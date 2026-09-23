package application_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
)

type completedPassportFunc func(context.Context, tenant.Scope, string) (passport.Passport, error)

func (f completedPassportFunc) LoadCompleted(c context.Context, s tenant.Scope, run string) (passport.Passport, error) {
	return f(c, s, run)
}

type passportSignFunc func(context.Context, passport.Passport) (passport.Passport, error)

func (f passportSignFunc) Sign(c context.Context, p passport.Passport) (passport.Passport, error) {
	return f(c, p)
}

func servicePassport(t *testing.T) passport.Passport {
	t.Helper()
	c := domain.Change{OrganizationID: "org_a", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	d := evidence.Digest([]byte("result"))
	p, err := passport.New(passport.Fields{Change: c, ChangeID: c.ID(), RunID: "run", Commit: c.HeadSHA, IntentDigest: d, ProofPlanDigest: d, EvidenceGraphDigest: d, ProofLevel: passport.UnknownLevel, PolicyDecision: passport.PolicyDecision{PolicyDigest: d, ResultDigest: d, Status: evidence.StatusUnknown}, VerificationDecision: trust.Unknown, IssuedAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostedPassportEntitlementAuthorizationAndHistory(t *testing.T) {
	for _, mode := range []string{"entitled", "not_entitled", "viewer", "foreign_tenant", "source_error", "foreign_source_org", "foreign_source_repo", "foreign_source_run", "sign_error", "sign_rewrite", "unsigned_result", "quota", "entitlement_error", "permission_error", "revoke_during_read", "cancel_during_read", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := servicePassport(t)
			original, _ := p.MarshalJSON()
			key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{5}, 32))
			granted, allowed := true, true
			sourceCalls, signCalls, quotaCalls := 0, 0, 0
			service, err := application.NewHostedPassportService(
				tenantFunc(func(_ context.Context, _ authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
					if mode == "foreign_tenant" {
						scope.OrganizationID = "other"
					}
					return scope, nil
				}),
				entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
					if org != "org_a" || k != feature.KeyProofPassport {
						t.Fatal("wrong entitlement")
					}
					if mode == "entitlement_error" {
						return false, errors.New("secret")
					}
					return granted, nil
				}),
				permissionFunc(func(_ context.Context, _ authorization.Principal, org string, k authorization.Key) (bool, error) {
					if org != "org_a" || k != authorization.KeyProofExecute {
						t.Fatal("wrong permission")
					}
					if mode == "permission_error" {
						return false, errors.New("secret")
					}
					return allowed, nil
				}),
				quotaFunc(func(context.Context, application.Execution) error {
					quotaCalls++
					if mode == "quota" {
						return errors.New("secret")
					}
					return nil
				}),
				completedPassportFunc(func(_ context.Context, scope tenant.Scope, run string) (passport.Passport, error) {
					sourceCalls++
					if scope.ResourceID != "repo" || run != "run" {
						t.Fatal("wrong source binding")
					}
					if mode == "source_error" {
						return passport.Passport{}, errors.New("secret")
					}
					if mode == "revoke_during_read" {
						granted = false
					}
					if mode == "cancel_during_read" {
						cancel()
					}
					f, _ := p.Fields()
					switch mode {
					case "foreign_source_org":
						f.Change.OrganizationID = "other"
						f.ChangeID = f.Change.ID()
					case "foreign_source_repo":
						f.Change.RepositoryID = "other"
						f.ChangeID = f.Change.ID()
					case "foreign_source_run":
						f.RunID = "other"
					}
					return passport.New(f)
				}),
				passportSignFunc(func(_ context.Context, input passport.Passport) (passport.Passport, error) {
					signCalls++
					if mode == "sign_error" {
						return passport.Passport{}, errors.New("secret")
					}
					if mode == "unsigned_result" {
						return input, nil
					}
					if mode == "sign_rewrite" {
						f, _ := input.Fields()
						f.RunID = "other"
						input, _ = passport.New(f)
					}
					return passport.Sign(input, "key_1", key)
				}))
			if err != nil {
				t.Fatal(err)
			}
			actor := authorization.Principal{ID: "owner", Kind: authorization.User}
			scope := tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"}
			var prior passport.Passport
			if mode == "downgrade" {
				prior, err = service.Sign(ctx, actor, scope, "run")
				if err != nil {
					t.Fatal(err)
				}
				granted = false
			}
			if mode == "not_entitled" {
				granted = false
			}
			if mode == "viewer" {
				allowed = false
			}
			result, err := service.Sign(ctx, actor, scope, "run")
			if mode == "entitled" {
				if err != nil || passport.Verify(result, "org_a", "key_1", key.Public().(ed25519.PublicKey)) != nil || sourceCalls != 1 || signCalls != 1 || quotaCalls != 1 {
					t.Fatal("signing failed or double quota", err, sourceCalls, signCalls, quotaCalls)
				}
				f, _ := result.Fields()
				if f.VerificationDecision != trust.Unknown {
					t.Fatal("signature promoted UNKNOWN")
				}
			} else if err == nil || result.Digest() != "" || strings.Contains(err.Error(), "secret") {
				t.Fatal("denial failed or leaked data", err)
			}
			if mode == "not_entitled" && (!errors.Is(err, entitlement.ErrNotEntitled) || sourceCalls != 0) {
				t.Fatal(err)
			}
			if mode == "viewer" && (!errors.Is(err, application.ErrPermissionDenied) || sourceCalls != 0) {
				t.Fatal(err)
			}
			if (mode == "revoke_during_read" || mode == "cancel_during_read") && signCalls != 0 {
				t.Fatal("key used after revocation/cancellation")
			}
			if mode == "downgrade" && (signCalls != 1 || passport.Verify(prior, "org_a", "key_1", key.Public().(ed25519.PublicKey)) != nil) {
				t.Fatal("downgrade invalidated immutable history")
			}
			now, _ := p.MarshalJSON()
			if !bytes.Equal(original, now) {
				t.Fatal("stored Passport changed")
			}
		})
	}
}
