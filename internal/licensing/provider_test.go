package licensing_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/licensing"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/tenant"
)

type source struct {
	mu    sync.Mutex
	state licensing.LicenseState
	err   error
	calls int
}

func (s *source) LoadLicense(ctx context.Context, org string) (licensing.LicenseState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err := ctx.Err(); err != nil {
		return licensing.LicenseState{}, err
	}
	if org != "org" {
		return licensing.LicenseState{}, errors.New("foreign source")
	}
	v := s.state
	v.Envelope = bytes.Clone(v.Envelope)
	return v, s.err
}
func (s *source) set(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.Revision++
	s.state.Envelope = bytes.Clone(raw)
	s.state.Revoked = false
}

type fixture struct {
	p       *licensing.EnterpriseLicenseEntitlementProvider
	source  *source
	doc     licensing.LicenseDocument
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	now     time.Time
	config  licensing.ProviderConfig
}

func sign(t testing.TB, key ed25519.PrivateKey, d licensing.LicenseDocument) []byte {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	message, err := licensing.SigningBytes("issuer", raw)
	if err != nil {
		t.Fatal(err)
	}
	envelope := licensing.SignedLicense{Schema: "safepatch.signed-license.v1", Algorithm: "Ed25519", KeyID: "issuer", Document: raw, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, message))}
	out, _ := json.Marshal(envelope)
	return out
}
func setup(t testing.TB) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), source: &source{}}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	f.public, f.private = pub, priv
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", f.now.Add(-24*time.Hour))
	editions, _ := edition.NewCatalog(catalog)
	for _, ed := range edition.Editions() {
		editions, err = editions.Publish(edition.EditionVersion{EditionVersionID: string(ed.Key) + "-2026-v1", EditionKey: ed.Key, CreatedAt: f.now.Add(-24 * time.Hour)}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	featureLimit := int64(5)
	f.doc = licensing.LicenseDocument{Schema: "safepatch.license.v1", LicenseID: "license_1", OrganizationID: "org", Edition: edition.Enterprise, EditionVersionID: "enterprise-2026-v1", Features: []licensing.FeatureGrant{{Key: feature.KeyVerifyBasic, Enabled: true}, {Key: feature.KeyRuntimeReplay, Enabled: true, LimitValue: &featureLimit}, {Key: feature.KeyEnterpriseSelfHosted, Enabled: true}}, Limits: []limits.EntitlementLimit{{Key: limits.LimitVerificationConcurrency, State: limits.Integer, Value: 2}, {Key: limits.LimitReplayRequestsMonthly, State: limits.Integer, Value: 3}}, IssuedAt: f.now.Add(-time.Hour), ExpiresAt: f.now.Add(time.Hour)}
	f.source.set(sign(t, priv, f.doc))
	f.config = licensing.ProviderConfig{OrganizationID: "org", PublicKeys: map[string]ed25519.PublicKey{"issuer": pub}, Source: f.source, Features: catalog, Editions: editions, Now: func() time.Time { return f.now }}
	f.p, err = licensing.NewEnterpriseLicenseEntitlementProvider(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestSignedLicenseFeedsSharedResolverAndLimits(t *testing.T) {
	f := setup(t)
	for _, key := range []feature.Key{feature.KeyVerifyBasic, feature.KeyRuntimeReplay, feature.KeyEnterpriseSelfHosted} {
		r, err := f.p.Resolve(t.Context(), "org", key)
		if err != nil || !r.Enabled || r.Source != limits.SourceEnterpriseLicense || r.Expiration == nil || !r.Expiration.Equal(f.doc.ExpiresAt) {
			t.Fatal(r, err)
		}
	}
	if enabled, err := f.p.Has(t.Context(), "org", feature.KeyEnterpriseAirgap); err != nil || enabled {
		t.Fatal("edition metadata granted unsigned feature")
	}
	if value, err := f.p.Limit(t.Context(), "org", feature.KeyRuntimeReplay); err != nil || value != 5 {
		t.Fatal(value, err)
	}
	if _, err := f.p.Limit(t.Context(), "org", feature.KeyVerifyBasic); !errors.Is(err, entitlement.ErrLimitUnspecified) {
		t.Fatal(err)
	}
	if l, err := f.p.ResourceLimit(t.Context(), "org", limits.LimitVerificationConcurrency); err != nil || l.Value != 2 {
		t.Fatal(l, err)
	}
	if l, err := f.p.ResourceLimit(t.Context(), "org", limits.LimitDevelopers); err != nil || l.State != limits.Disabled {
		t.Fatal(l, err)
	}
	v, err := f.p.Inspect(t.Context(), "org")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := v.Document()
	d.Features[0].Enabled = false
	d.Limits[0].Value = 999
	again, _ := v.Document()
	if again.Features[0].Enabled == false || again.Limits[0].Value == 999 {
		t.Fatal("mutable inspection")
	}
	// The caller cannot mutate the provider's trusted public key bytes.
	f.public[0] ^= 255
	if enabled, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic); err != nil || !enabled {
		t.Fatal("public key alias", err)
	}
}
func TestLicenseForgeryExpiryAndValidation(t *testing.T) {
	for _, mode := range []string{"signature", "wrong_key", "algorithm", "wrong_org", "future", "expired", "unknown_feature", "duplicate_feature", "unknown_limit", "negative_limit", "wrong_edition_version", "null_features", "duplicate_key", "extra_key", "key_url", "oversize", "revoked", "source_error"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			d := f.doc
			raw := bytes.Clone(f.source.state.Envelope)
			switch mode {
			case "signature":
				var e licensing.SignedLicense
				_ = json.Unmarshal(raw, &e)
				e.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
				raw, _ = json.Marshal(e)
			case "wrong_key":
				_, private, _ := ed25519.GenerateKey(nil)
				raw = sign(t, private, d)
			case "algorithm":
				raw = bytes.Replace(raw, []byte(`"algorithm":"Ed25519"`), []byte(`"algorithm":"none"`), 1)
			case "wrong_org":
				d.OrganizationID = "other"
				raw = sign(t, f.private, d)
			case "future":
				d.IssuedAt = f.now.Add(time.Minute)
				raw = sign(t, f.private, d)
			case "expired":
				d.ExpiresAt = f.now
				raw = sign(t, f.private, d)
			case "unknown_feature":
				d.Features = append(d.Features, licensing.FeatureGrant{Key: "unknown.feature", Enabled: true})
				raw = sign(t, f.private, d)
			case "duplicate_feature":
				d.Features = append(d.Features, d.Features[0])
				raw = sign(t, f.private, d)
			case "unknown_limit":
				d.Limits = append(d.Limits, limits.EntitlementLimit{Key: "unknown", State: limits.Unlimited})
				raw = sign(t, f.private, d)
			case "negative_limit":
				d.Limits[0].Value = -1
				raw = sign(t, f.private, d)
			case "wrong_edition_version":
				d.EditionVersionID = "team-2026-v1"
				raw = sign(t, f.private, d)
			case "null_features":
				d.Features = nil
				raw = sign(t, f.private, d)
			case "duplicate_key":
				raw = bytes.Replace(raw, []byte(`"algorithm":`), []byte(`"algorithm":"none","algorithm":`), 1)
			case "extra_key":
				raw = append([]byte(`{"public_key":"attacker",`), raw[1:]...)
			case "key_url":
				raw = bytes.Replace(raw, []byte(`"key_id":"issuer"`), []byte(`"key_id":"https://attacker/key"`), 1)
			case "oversize":
				raw = bytes.Repeat([]byte("x"), licensing.MaxLicenseBytes+1)
			}
			f.source.set(raw)
			if mode == "revoked" {
				f.source.state.Revoked = true
			}
			if mode == "source_error" {
				f.source.err = errors.New("private connection credential")
			}
			if allowed, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic); err == nil || allowed || bytes.Contains([]byte(err.Error()), []byte("private")) {
				t.Fatal("invalid license allowed", allowed, err)
			}
		})
	}
}
func TestSourceRevisionClockAndCancellation(t *testing.T) {
	for _, mode := range []string{"rollback", "same_revision_changed", "clock_rollback", "expiry_no_cache", "cancel", "foreign_org"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			if _, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			org := "org"
			switch mode {
			case "rollback":
				f.source.state.Revision = 0
			case "same_revision_changed":
				f.source.state.Revoked = true
			case "clock_rollback":
				f.now = f.now.Add(-time.Second)
			case "expiry_no_cache":
				f.now = f.doc.ExpiresAt
			case "cancel":
				cancel()
			case "foreign_org":
				org = "other"
			}
			if allowed, err := f.p.Has(ctx, org, feature.KeyVerifyBasic); err == nil || allowed {
				t.Fatal(mode, allowed, err)
			}
		})
	}
}

type tenants struct{}

func (tenants) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if s.OrganizationID != "org" || s.ResourceID != "repo" {
		return tenant.Scope{}, errors.New("foreign")
	}
	return s, nil
}

type membership struct{ role authorization.Role }

func (m membership) GetMembership(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
	return authorization.Membership{OrganizationID: org, Principal: p, Role: m.role, Active: true}, nil
}

type quotas struct{}

func (quotas) Check(context.Context, application.Execution) error { return nil }
func TestLicenseDoesNotReplaceUserAuthorization(t *testing.T) {
	for _, mode := range []string{"entitled", "not_entitled", "viewer"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			role := authorization.RoleOwner
			if mode == "viewer" {
				role = authorization.RoleViewer
			}
			if mode == "not_entitled" {
				f.doc.Features = []licensing.FeatureGrant{}
				f.source.set(sign(t, f.private, f.doc))
			}
			permissions, _ := authorization.NewPermissionService(membership{role})
			guard, err := application.NewExecutionGuard(tenants{}, f.p, permissions, quotas{}, application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute})
			if err != nil {
				t.Fatal(err)
			}
			_, err = guard.Authorize(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
			if (mode == "entitled") != (err == nil) {
				t.Fatal(err)
			}
		})
	}
}
func TestRenewalDowngradePreservesQuotaAndSettlesAfterExpiry(t *testing.T) {
	f := setup(t)
	ledger, _ := quotastore.NewMemory(2, 32)
	licensed, _ := licensing.NewLicensedQuotaStore(f.p, ledger)
	service, _ := entitlement.NewQuotaService(licensed, func() time.Time { return f.now })
	prior, err := service.Reserve(t.Context(), "org", limits.LimitVerificationConcurrency, 2, "prior")
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.p.Inspect(t.Context(), "org")
	if err != nil {
		t.Fatal(err)
	}
	oldDoc, _ := old.Document()
	before, _ := json.Marshal(oldDoc)
	f.doc.LicenseID = "license_2"
	f.doc.Features[1].Enabled = false
	f.doc.Limits[0].Value = 1
	f.source.set(sign(t, f.private, f.doc))
	if enabled, err := f.p.Has(t.Context(), "org", feature.KeyRuntimeReplay); err != nil || enabled {
		t.Fatal("downgrade ignored")
	}
	if _, err := service.Reserve(t.Context(), "org", limits.LimitVerificationConcurrency, 1, "over"); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal(err)
	}
	_ = ledger.WithinOrganization(t.Context(), "org", func(tx entitlement.QuotaTransaction) error {
		stored, err := tx.Find("prior")
		if err != nil || stored != prior {
			t.Fatal("history rewritten", err)
		}
		return nil
	})
	oldDoc, _ = old.Document()
	after, _ := json.Marshal(oldDoc)
	if !bytes.Equal(before, after) {
		t.Fatal("license inspection changed")
	}
	f.now = f.doc.ExpiresAt
	if _, err := service.Reserve(t.Context(), "org", limits.LimitVerificationConcurrency, 1, "expired"); err == nil {
		t.Fatal("expired quota admitted")
	}
	if _, err := service.Release(t.Context(), "org", "prior"); err != nil {
		t.Fatal("expiry stranded cleanup", err)
	}
}
func TestConcurrentVerificationAndAllEditionMetadata(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic); err != nil || !ok {
				t.Error(ok, err)
			}
		}()
	}
	wg.Wait()
	for _, ed := range edition.Editions() {
		f.doc.Edition = ed.Key
		f.doc.EditionVersionID = string(ed.Key) + "-2026-v1"
		f.source.set(sign(t, f.private, f.doc))
		if ok, err := f.p.Has(t.Context(), "org", feature.KeyVerifyBasic); err != nil || !ok {
			t.Fatal("edition branch", ed, err)
		}
	}
}
func TestPrivateKeyMaterialRejectedAsVerificationKey(t *testing.T) {
	f := setup(t)
	f.config.PublicKeys = map[string]ed25519.PublicKey{"issuer": ed25519.PublicKey(f.private)}
	if _, err := licensing.NewEnterpriseLicenseEntitlementProvider(f.config); err == nil {
		t.Fatal("accepted 64-byte private key")
	}
}

func TestZeroValueProvidersFailClosed(t *testing.T) {
	var p licensing.EnterpriseLicenseEntitlementProvider
	if allowed, err := p.Has(t.Context(), "org", feature.KeyVerifyBasic); allowed || err == nil {
		t.Fatal("zero provider admitted")
	}
	var q licensing.LicensedQuotaStore
	if err := q.WithinOrganization(t.Context(), "org", func(entitlement.QuotaTransaction) error { t.Fatal("zero quota entered"); return nil }); err == nil {
		t.Fatal("zero quota admitted")
	}
}
func FuzzLicenseEnvelope(f *testing.F) {
	fixture := setup(f)
	f.Add(bytes.Clone(fixture.source.state.Envelope))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		fixture.source.set(raw)
		_, _ = fixture.p.Has(t.Context(), "org", feature.KeyVerifyBasic)
	})
}
