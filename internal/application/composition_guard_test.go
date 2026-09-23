package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/proof/composition"
	"safepatch/internal/proof/passport"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
)

type compositionSourceFuncs struct {
	manifest func(context.Context, string, string) (composition.Manifest, error)
	evidence func(context.Context, composition.Manifest) (composition.Input, error)
}

func (s compositionSourceFuncs) Manifest(c context.Context, o, id string) (composition.Manifest, error) {
	return s.manifest(c, o, id)
}
func (s compositionSourceFuncs) Evidence(c context.Context, m composition.Manifest) (composition.Input, error) {
	return s.evidence(c, m)
}

func TestCompositionServiceChecksEveryRepositoryAndCurrentAccess(t *testing.T) {
	for _, mode := range []string{"allowed", "unentitled", "legacy_key_only", "viewer", "entitlement_error", "permission_error", "quota", "foreign_org", "foreign_repo", "wrong_id", "source_error", "evidence_error", "revoke_during_read", "cancel", "clock_rollback", "slow_final_permission"} {
		t.Run(mode, func(t *testing.T) {
			base := servicePassport(t)
			fields, _ := base.Fields()
			fields.ProofLevel = proof.L2
			fields.PolicyDecision.Status = evidence.StatusPass
			fields.VerificationDecision = trust.Pass
			p, _ := passport.New(fields)
			other := fields
			other.Change.RepositoryID = "repo_b"
			other.ChangeID = other.Change.ID()
			p2, _ := passport.New(other)
			now := fields.IssuedAt
			digest := evidence.Digest([]byte("combined verification"))
			mf := composition.Fields{Schema: composition.ManifestSchema, ID: "set", OrganizationID: "org_a", PolicyDigest: digest, Components: []composition.Component{{ID: "a", RepositoryID: "repo", Commit: fields.Commit, PassportDigest: p.Digest()}, {ID: "b", RepositoryID: "repo_b", Commit: other.Commit, PassportDigest: p2.Digest()}}, Connections: []composition.Connection{{ID: "api", From: "a", To: "b", ContractDigest: digest}}}
			m, _ := composition.New(mf)
			a := composition.Attestation{SubjectID: "api", ManifestDigest: m.Digest(), PolicyDigest: digest, EvidenceDigest: digest, Status: evidence.StatusPass, IssuedAt: now, ValidUntil: now.Add(time.Hour)}
			combined := a
			combined.SubjectID = "set"
			input := composition.Input{Components: []composition.ComponentProof{{ComponentID: "a", Passport: p, ValidUntil: now.Add(time.Hour)}, {ComponentID: "b", Passport: p2, ValidUntil: now.Add(time.Hour)}}, Connections: []composition.Attestation{a}, Combined: &combined}
			granted := mode != "unentitled" && mode != "legacy_key_only"
			manifestCalls, evidenceCalls, quotaCalls, permissionCalls := 0, 0, 0, 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			service, err := application.NewCompositionGuard(
				tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if s.OrganizationID != "org_a" || (s.ResourceID != "org_a" && s.ResourceID != "repo" && s.ResourceID != "repo_b") {
						return tenant.Scope{}, tenant.ErrInvalidScope
					}
					if mode == "foreign_repo" && s.ResourceID == "repo_b" {
						s.OrganizationID = "victim"
					}
					return s, nil
				}),
				entitlementFunc(func(_ context.Context, org string, key feature.Key) (bool, error) {
					if key != feature.KeyProofComposition || org != "org_a" {
						t.Fatal("wrong grant lookup")
					}
					if mode == "entitlement_error" {
						return false, errors.New("internal")
					}
					return granted, nil
				}),
				permissionFunc(func(_ context.Context, _ authorization.Principal, org string, key authorization.Key) (bool, error) {
					permissionCalls++
					if key != authorization.KeyProofExecute || org != "org_a" {
						t.Fatal("wrong permission")
					}
					if mode == "permission_error" {
						return false, errors.New("internal")
					}
					if mode == "slow_final_permission" && permissionCalls == 5 {
						now = now.Add(time.Minute)
					}
					return mode != "viewer", nil
				}),
				quotaFunc(func(context.Context, application.Execution) error {
					quotaCalls++
					if mode == "quota" {
						return errors.New("internal")
					}
					return nil
				}),
				compositionSourceFuncs{
					manifest: func(_ context.Context, org, id string) (composition.Manifest, error) {
						manifestCalls++
						if org != "org_a" || id != "set" {
							t.Fatal("scope")
						}
						if mode == "source_error" {
							return composition.Manifest{}, errors.New("secret-token")
						}
						if mode == "foreign_org" {
							mf.OrganizationID = "victim"
							return composition.New(mf)
						}
						if mode == "wrong_id" {
							mf.ID = "other"
							return composition.New(mf)
						}
						return m, nil
					},
					evidence: func(_ context.Context, loaded composition.Manifest) (composition.Input, error) {
						evidenceCalls++
						if loaded.Digest() != m.Digest() {
							t.Fatal("manifest changed")
						}
						if mode == "evidence_error" {
							return composition.Input{}, errors.New("secret-token")
						}
						if mode == "revoke_during_read" {
							granted = false
						}
						if mode == "cancel" {
							cancel()
						}
						if mode == "clock_rollback" {
							now = now.Add(-time.Second)
						}
						return input, nil
					},
				}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			r, err := service.Evaluate(ctx, authorization.Principal{ID: "owner", Kind: authorization.User}, "org_a", "set")
			if mode == "allowed" {
				f, e := r.Fields()
				if err != nil || e != nil || f.Decision != trust.Pass || manifestCalls != 1 || evidenceCalls != 1 || quotaCalls != 1 || permissionCalls != 5 {
					t.Fatal(f, err, permissionCalls)
				}
				return
			}
			if err == nil || r.Digest() != "" || strings.Contains(err.Error(), "secret-token") {
				t.Fatal("unexpected success or exposure", err)
			}
			if mode == "foreign_repo" && evidenceCalls != 0 {
				t.Fatal("read unauthorized repository proofs")
			}
			if (mode == "unentitled" || mode == "legacy_key_only" || mode == "viewer" || mode == "quota") && manifestCalls != 0 {
				t.Fatal("early guard bypass")
			}
		})
	}
}
