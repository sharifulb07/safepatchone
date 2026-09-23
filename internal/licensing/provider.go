package licensing

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"slices"
	"time"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
)

// LicenseState is a complete authoritative installation snapshot. Revision must
// increase atomically for replacement, removal and revocation. Envelope bytes are
// untrusted; only configured public keys establish their issuance authority.
type LicenseState struct {
	Revision uint64
	Revoked  bool
	Envelope []byte
	// ConfirmedAt is trusted installation/refresh metadata, never supplied by a
	// license document. RefreshDenied fences non-transient refresh failures.
	ConfirmedAt   time.Time
	RefreshDenied bool
}
type LicenseSource interface {
	LoadLicense(context.Context, string) (LicenseState, error)
}
type ProviderConfig struct {
	OrganizationID string
	PublicKeys     map[string]ed25519.PublicKey
	Source         LicenseSource
	Features       *feature.FeatureCatalog
	Editions       *edition.Catalog
	Now            func() time.Time
	Deployment     DeploymentPolicy
}
type EnterpriseLicenseEntitlementProvider struct {
	org         string
	keys        map[string]ed25519.PublicKey
	source      LicenseSource
	features    *feature.FeatureCatalog
	editions    *edition.Catalog
	now         func() time.Time
	lock        chan struct{}
	revision    uint64
	stateDigest string
	lastTime    time.Time
	deployment  DeploymentPolicy
}

var _ entitlement.EntitlementService = (*EnterpriseLicenseEntitlementProvider)(nil)

func NewEnterpriseLicenseEntitlementProvider(c ProviderConfig) (*EnterpriseLicenseEntitlementProvider, error) {
	if err := c.Deployment.Validate(); err != nil {
		return nil, err
	}
	if !domain.ValidID(c.OrganizationID) || c.Source == nil || c.Features == nil || c.Editions == nil || c.Now == nil || len(c.PublicKeys) == 0 || len(c.PublicKeys) > 32 {
		return nil, ErrInvalidLicense
	}
	keys := map[string]ed25519.PublicKey{}
	for id, key := range c.PublicKeys {
		if !domain.ValidID(id) || len(key) != ed25519.PublicKeySize {
			return nil, ErrInvalidLicense
		}
		keys[id] = slices.Clone(key)
	}
	return &EnterpriseLicenseEntitlementProvider{org: c.OrganizationID, keys: keys, source: c.Source, features: c.Features, editions: c.Editions, now: c.Now, lock: make(chan struct{}, 1), deployment: c.Deployment}, nil
}
func (p *EnterpriseLicenseEntitlementProvider) inspect(ctx context.Context, org string) (VerifiedLicense, time.Time, error) {
	if p == nil || ctx == nil || p.lock == nil || p.source == nil || p.features == nil || p.editions == nil || p.now == nil || len(p.keys) == 0 {
		return VerifiedLicense{}, time.Time{}, ErrInvalidLicense
	}
	if err := ctx.Err(); err != nil {
		return VerifiedLicense{}, time.Time{}, err
	}
	if org != p.org {
		return VerifiedLicense{}, time.Time{}, entitlement.ErrOrganizationMismatch
	}
	select {
	case p.lock <- struct{}{}:
	case <-ctx.Done():
		return VerifiedLicense{}, time.Time{}, ctx.Err()
	}
	defer func() { <-p.lock }()
	if err := ctx.Err(); err != nil {
		return VerifiedLicense{}, time.Time{}, err
	}
	state, err := p.source.LoadLicense(ctx, org)
	if err != nil {
		return VerifiedLicense{}, time.Time{}, ErrLicenseUnavailable
	}
	if err := ctx.Err(); err != nil {
		return VerifiedLicense{}, time.Time{}, err
	}
	now := p.now().UTC()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 || now.Before(p.lastTime) {
		return VerifiedLicense{}, time.Time{}, ErrInvalidLicense
	}
	p.lastTime = now
	if state.Revision == 0 || len(state.Envelope) > MaxLicenseBytes {
		return VerifiedLicense{}, now, ErrInvalidLicense
	}
	raw, _ := json.Marshal(struct {
		Revoked       bool
		Digest        string
		ConfirmedAt   time.Time
		RefreshDenied bool
	}{state.Revoked, evidence.Digest(state.Envelope), state.ConfirmedAt, state.RefreshDenied})
	hash := evidence.Digest(raw)
	if state.Revision < p.revision || state.Revision == p.revision && hash != p.stateDigest {
		return VerifiedLicense{}, now, ErrInvalidLicense
	}
	p.revision = state.Revision
	p.stateDigest = hash
	if state.Revoked || state.RefreshDenied {
		return VerifiedLicense{}, now, ErrInvalidLicense
	}
	v, err := verify(state.Envelope, p.keys, p.features, p.editions, org, now)
	if err != nil {
		return VerifiedLicense{}, now, err
	}
	v.availability, err = p.availability(ctx, v, state, now)
	if err != nil {
		return VerifiedLicense{}, now, err
	}
	return v, now, nil
}
func (p *EnterpriseLicenseEntitlementProvider) Inspect(ctx context.Context, org string) (VerifiedLicense, error) {
	v, _, err := p.inspect(ctx, org)
	return v, err
}
func (p *EnterpriseLicenseEntitlementProvider) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	r, err := p.Resolve(ctx, org, key)
	return err == nil && r.Enabled, err
}
func (p *EnterpriseLicenseEntitlementProvider) Limit(ctx context.Context, org string, key feature.Key) (int64, error) {
	r, err := p.Resolve(ctx, org, key)
	if err != nil {
		return 0, err
	}
	if !r.Enabled {
		return 0, entitlement.ErrNotEntitled
	}
	if r.EffectiveLimit == nil {
		return 0, entitlement.ErrLimitUnspecified
	}
	return *r.EffectiveLimit, nil
}
func (p *EnterpriseLicenseEntitlementProvider) Resolve(ctx context.Context, org string, key feature.Key) (entitlement.Resolution, error) {
	v, now, err := p.inspect(ctx, org)
	if err != nil {
		return entitlement.Resolution{}, err
	}
	r, err := p.resolveLicense(ctx, v, key, now)
	if err == nil && (r.Expiration == nil || v.availability.ValidUntil.Before(*r.Expiration)) {
		until := v.availability.ValidUntil
		r.Expiration = &until
	}
	return r, err
}
func (p *EnterpriseLicenseEntitlementProvider) resolveLicense(ctx context.Context, v VerifiedLicense, key feature.Key, now time.Time) (entitlement.Resolution, error) {
	records, err := p.records(v)
	if err != nil {
		return entitlement.Resolution{}, err
	}
	resolver, err := entitlement.NewEntitlementResolver(licenseRepository{p.org, records}, p.features, p.now)
	if err != nil {
		return entitlement.Resolution{}, err
	}
	return resolver.ResolveAt(ctx, p.org, key, now)
}
func (p *EnterpriseLicenseEntitlementProvider) records(v VerifiedLicense) ([]domain.Entitlement, error) {
	d, err := v.Document()
	if err != nil {
		return nil, err
	}
	out := []domain.Entitlement{}
	metadata, _ := json.Marshal(map[string]string{"license_digest": v.Digest(), "key_id": v.KeyID(), "edition_version_id": d.EditionVersionID})
	for _, g := range d.Features {
		f, _ := p.features.Get(g.Key)
		record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "license_" + evidence.Digest([]byte(d.LicenseID+"\n"+f.FeatureID)), OrganizationID: d.OrganizationID, FeatureID: f.FeatureID, Source: domain.SourceEnterpriseLicense, SourceReference: d.LicenseID, Enabled: g.Enabled, LimitValue: g.LimitValue, StartsAt: d.IssuedAt, ExpiresAt: &d.ExpiresAt, CreatedAt: d.IssuedAt, Metadata: metadata}, p.features)
		if err != nil {
			return nil, ErrInvalidLicense
		}
		out = append(out, record)
	}
	return out, nil
}

// ResourceLimit uses the existing explicit INTEGER/UNLIMITED/DISABLED model.
// Missing resources are disabled. Feature Limit retains its separate contract.
func (p *EnterpriseLicenseEntitlementProvider) ResourceLimit(ctx context.Context, org string, key domain.LimitKey) (domain.EntitlementLimit, error) {
	denied := domain.EntitlementLimit{Key: key, State: domain.Disabled}
	if _, err := key.Accounting(); err != nil {
		return denied, err
	}
	v, _, err := p.inspect(ctx, org)
	if err != nil {
		return denied, err
	}
	d, _ := v.Document()
	for _, l := range d.Limits {
		if l.Key == key {
			return l, nil
		}
	}
	return denied, nil
}

type licenseRepository struct {
	org     string
	records []domain.Entitlement
}

func (licenseRepository) Create(context.Context, string, domain.Entitlement) error {
	return ErrInvalidLicense
}
func (r licenseRepository) Get(ctx context.Context, org, id string) (domain.Entitlement, error) {
	if ctx == nil || entitlement.ValidateGet(org, id) != nil {
		return domain.Entitlement{}, entitlement.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return domain.Entitlement{}, err
	}
	if org == r.org {
		for _, e := range r.records {
			if e.EntitlementID == id {
				return e.Clone(), nil
			}
		}
	}
	return domain.Entitlement{}, entitlement.ErrNotFound
}
func (r licenseRepository) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
	result := entitlement.Page{Entitlements: []domain.Entitlement{}}
	if ctx == nil || entitlement.ValidateList(org, q) != nil {
		return result, entitlement.ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if org != r.org {
		return result, entitlement.ErrOrganizationMismatch
	}
	records := slices.Clone(r.records)
	slices.SortFunc(records, func(a, b domain.Entitlement) int {
		if a.EntitlementID < b.EntitlementID {
			return -1
		}
		if a.EntitlementID > b.EntitlementID {
			return 1
		}
		return 0
	})
	for _, e := range records {
		if e.EntitlementID <= q.AfterID || q.FeatureID != "" && e.FeatureID != q.FeatureID {
			continue
		}
		if len(result.Entitlements) == q.Limit {
			result.NextAfterID = result.Entitlements[len(result.Entitlements)-1].EntitlementID
			break
		}
		result.Entitlements = append(result.Entitlements, e.Clone())
	}
	return result, nil
}
