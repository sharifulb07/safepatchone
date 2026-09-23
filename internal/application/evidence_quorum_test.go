package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/tenant"
	"testing"
	"time"
)

type quorumSource struct {
	calls  int
	mode   string
	onLoad func()
}

func (s *quorumSource) Load(_ context.Context, scope tenant.Scope, policyID, changeID, runID string) (quorum.QuorumPolicy, quorum.Input, error) {
	s.calls++
	if s.onLoad != nil {
		s.onLoad()
	}
	if s.mode == "source_error" {
		return quorum.QuorumPolicy{}, quorum.Input{}, errors.New("private credential")
	}
	p, _ := quorum.New(quorum.PolicyFields{Schema: quorum.Schema, ID: policyID, Version: "1", Sources: []quorum.Source{{ID: "unit", Producer: "unit", Type: "tests", Family: quorum.Tests, IndependenceID: "unit", Capability: "go.test"}}, Requirements: []quorum.QuorumRequirement{{ID: "correctness", Sources: []string{"unit"}, RequiredFamilies: 1}}})
	in := quorum.Input{TenantID: scope.OrganizationID, RepositoryID: scope.ResourceID, ChangeID: changeID, RunID: runID, Complete: true}
	switch s.mode {
	case "foreign_org":
		in.TenantID = "foreign"
	case "foreign_repo":
		in.RepositoryID = "foreign"
	case "foreign_run":
		in.RunID = "foreign"
	case "foreign_change":
		in.ChangeID = "foreign"
	case "wrong_policy":
		p = quorum.QuorumPolicy{}
	}
	n, err := evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: "tests", TenantID: in.TenantID, ChangeID: changeID, RunID: runID, Producer: "unit", ProducerVersion: "1.0.0", Category: "verification", Type: "tests", Deterministic: true, Status: evidence.StatusPass, Severity: evidence.SeverityInfo, Confidence: 1, Summary: "fixture observation", Payload: json.RawMessage(`{}`), CreatedAt: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		return quorum.QuorumPolicy{}, quorum.Input{}, err
	}
	in.Evidence = []evidence.Evidence{n}
	return p, in, nil
}

func TestEvidenceQuorumEntitlementMatrixAndBoundaries(t *testing.T) {
	var reference string
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			for _, mode := range []string{"allowed", "tenant", "not_entitled", "entitlement_error", "not_permitted", "quota", "source_error", "foreign_org", "foreign_repo", "foreign_run", "foreign_change", "wrong_policy", "cancel", "revoke_during_load"} {
				t.Run(mode, func(t *testing.T) {
					enabled := mode != "not_entitled"
					permissionCalls, quotaCalls := 0, 0
					source := &quorumSource{mode: mode}
					if mode == "revoke_during_load" {
						source.onLoad = func() { enabled = false }
					}
					service, err := application.NewEvidenceQuorumService(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
						if mode == "tenant" {
							s.OrganizationID = "foreign"
						}
						return s, nil
					}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
						if org != "org" || k != feature.KeyProofQuorum {
							t.Fatal("wrong commercial boundary")
						}
						if mode == "entitlement_error" {
							return true, errors.New("private error")
						}
						return enabled, nil
					}), permissionFunc(func(_ context.Context, _ authorization.Principal, _ string, k authorization.Key) (bool, error) {
						permissionCalls++
						if k != authorization.KeyProofExecute {
							t.Fatal("wrong authorization")
						}
						return mode != "not_permitted", nil
					}), quotaFunc(func(_ context.Context, e application.Execution) error {
						quotaCalls++
						if e.Requirement.Feature != feature.KeyProofQuorum {
							t.Fatal("wrong quota feature")
						}
						if mode == "quota" {
							return errors.New("private quota")
						}
						return nil
					}), source, func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) })
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					if mode == "cancel" {
						cancel()
					}
					call := func() (quorum.QuorumResult, error) {
						return service.Evaluate(ctx, authorization.Principal{ID: "owner", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, "payment", "change", "run")
					}
					r, err := call()
					if mode == "allowed" {
						if err != nil {
							t.Fatal(err)
						}
						f, _ := r.Fields()
						if f.Decision != quorum.Satisfied || quotaCalls != 1 {
							t.Fatal(f, quotaCalls)
						}
						if reference == "" {
							reference = r.Digest()
						}
						if reference != r.Digest() {
							t.Fatal("edition changed semantics")
						}
						enabled = false
						if denied, err := call(); err == nil || denied.Digest() != "" {
							t.Fatal("revoked grant allowed")
						}
						enabled = true
						if restored, err := call(); err != nil || restored.Digest() != r.Digest() {
							t.Fatal("grant restoration changed proof", err)
						}
						return
					}
					if err == nil || r.Digest() != "" {
						t.Fatal("failed admission produced proof", r, err)
					}
					if mode == "not_entitled" && (permissionCalls != 0 || quotaCalls != 0) {
						t.Fatal("admission order changed")
					}
					if (mode == "tenant" || mode == "not_entitled" || mode == "not_permitted" || mode == "quota" || mode == "cancel") && source.calls != 0 {
						t.Fatal("source accessed before admission")
					}
				})
			}
		})
	}
}

func TestQuorumRegistrationDoesNotGrantCommunityAccess(t *testing.T) {
	catalog, err := feature.NewBuiltinCatalog("0.1.0", edition.CommunityBundleRegisteredAt())
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := edition.NewCommunityBundle(catalog)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(bundle)
	var v any
	if json.Unmarshal(raw, &v) != nil {
		t.Fatal("invalid bundle")
	}
	// The pinned existing Community bundle test also checks every grant. Compare
	// this new key with the independently published mappings rather than grant it.
	f, err := catalog.Get(feature.KeyProofQuorum)
	if err != nil || f.FeatureID != "feat_0046" {
		t.Fatal(f, err)
	}
	for _, m := range bundle.Features {
		if m.FeatureID == f.FeatureID && m.Enabled {
			t.Fatal("registration silently granted advanced access")
		}
	}
}
