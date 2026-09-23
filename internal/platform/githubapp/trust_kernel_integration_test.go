package githubapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/config"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/policy"
	"safepatch/internal/proof"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"testing"
	"time"
)

type assessmentSource struct {
	calls int
	mode  string
}

func (s *assessmentSource) AssessAuthorization(_ context.Context, in application.AuthorizationSubject) (trust.Assessment, error) {
	s.calls++
	if s.mode == "error" {
		return trust.Assessment{}, errors.New("private provenance credential")
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	risk := 10
	out := trust.Assessment{Risk: &risk, BusinessImpact: policy.CriticalityLow, ProofFreshness: trust.Freshness{VerifiedAt: now, ExpiresAt: now.Add(time.Minute)}, Provenance: trust.Provenance{Status: evidence.StatusPass, Origin: trust.OriginVerifier, ChangeID: in.Change.ID(), RunID: in.RunID, IntentDigest: in.IntentDigest, EvidenceDigest: in.EvidenceDigest, PolicyDigest: in.PolicyDigest}}
	switch s.mode {
	case "risk":
		*out.Risk = 90
	case "impact":
		out.BusinessImpact = policy.CriticalityCritical
	case "llm":
		out.Provenance.Origin = trust.OriginLLM
	case "expired":
		// The fixture clock advances after assessment to exercise expiry.
	case "foreign":
		out.Provenance.ChangeID = "other"
	}
	return out, nil
}
func TestTrustKernelEveryEditionSharedWorkflow(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Now().UTC())
	matrix, err := config.EarnedAutonomyEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range matrix.Editions {
		t.Run(string(e.Bundle.Version.EditionKey), func(t *testing.T) {
			source := &assessmentSource{}
			access := &changeAccess{allowed: true, quota: true}
			grants := flowEntitlements{enabled: map[feature.Key]bool{}}
			for _, row := range e.Bundle.Features {
				f, err := fc.GetByID(row.FeatureID)
				if err != nil {
					t.Fatal(err)
				}
				grants.enabled[f.Key] = row.Enabled
			}
			tenants := tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				return s, nil
			})
			core, change, _, _, archive, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) {
				c.FinalAuthorization = &application.FinalAuthorizationConfig{Controls: trust.DefaultControls(proof.L0), Source: source}
				c.Guard, _ = application.NewExecutionGuard(tenants, grants, access, access, application.Requirement{Feature: feature.KeyVerifyBasic, Permission: authorization.KeyProofExecute})
				c.StepGuard = func(ctx context.Context, a authorization.Principal, s tenant.Scope, r application.Requirement) error {
					g, err := application.NewExecutionGuard(tenants, grants, access, access, r)
					if err != nil {
						return err
					}
					_, err = g.Authorize(ctx, a, s)
					return err
				}
			})
			actor := authorization.Principal{ID: "user", Kind: authorization.User}
			local, err := core.Run(t.Context(), actor, change, "run")
			if err != nil || local.Authorization != trust.Authorize {
				t.Fatal(local, err)
			}
			hosted, err := core.RunHosted(t.Context(), actor, change, "run")
			if err != nil || local != hosted {
				t.Fatal("different kernel by flow", local, hosted, err)
			}
			if len(archive.saved) != 2 || archive.saved[0].Authorization.InputDigest == "" || archive.saved[0].Authorization.InputDigest != archive.saved[1].Authorization.InputDigest {
				t.Fatal("decision not retained")
			}
			access.allowed = false
			result, err := core.Run(t.Context(), actor, change, "denied")
			if err == nil || result.Authorization != trust.Block || source.calls != 2 {
				t.Fatal("technical authorization bypassed permissions", result, err)
			}
			access.allowed = true
			before, _ := json.Marshal(archive.saved)
			grants.enabled[feature.KeyVerifyBasic] = false
			result, err = core.Run(t.Context(), actor, change, "downgrade")
			after, _ := json.Marshal(archive.saved)
			if err == nil || result.Authorization != trust.Block || source.calls != 2 || string(before) != string(after) {
				t.Fatal("entitlement bypass or history changed", result, err)
			}
			grants.enabled[feature.KeyVerifyBasic] = true
			result, err = core.Run(t.Context(), actor, change, "run")
			if err != nil || result.Authorization != trust.Authorize || archive.saved[2].Authorization.InputDigest != archive.saved[0].Authorization.InputDigest {
				t.Fatal("restored plan changed technical judgment", result, err)
			}
		})
	}
}

func TestWorkflowDoesNotReturnAuthorizationAfterPersistenceFailure(t *testing.T) {
	src := &assessmentSource{}
	core, change, _, _, archive, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) {
		c.FinalAuthorization = &application.FinalAuthorizationConfig{Controls: trust.DefaultControls(proof.L0), Source: src}
	})
	archive.fail = true
	r, err := core.Run(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "run")
	if err == nil || r.Authorization != trust.Block || r.Decision != trust.Unknown || len(archive.saved) != 0 {
		t.Fatal("unpersisted authorization leaked", r, err)
	}
}
func TestWorkflowFinalAuthorizationOutcomes(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want trust.AuthorizationDecision
	}{{"risk", trust.Review}, {"impact", trust.Escrow}, {"llm", trust.Block}, {"expired", trust.Reverify}, {"foreign", trust.Block}, {"error", trust.Block}} {
		t.Run(tc.mode, func(t *testing.T) {
			src := &assessmentSource{mode: tc.mode}
			core, change, _, _, archive, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) {
				c.FinalAuthorization = &application.FinalAuthorizationConfig{Controls: trust.DefaultControls(proof.L0), Source: src}
				if tc.mode == "expired" {
					c.Now = func() time.Time {
						now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
						if src.calls > 0 {
							return now.Add(2 * time.Minute)
						}
						return now
					}
				}
			})
			r, err := core.Run(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "run")
			if r.Authorization != tc.want || (err != nil) != (tc.mode == "error") {
				t.Fatal(r, err)
			}
			if tc.mode != "error" && (r.Decision != trust.Pass || len(archive.saved) != 1) {
				t.Fatal("diagnostic schema conflated with authorization")
			}
		})
	}
	core, change, _, _, _, _ := workflowFixture(t)
	r, err := core.Run(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "run")
	if err != nil || r.Authorization != trust.Reverify || r.Decision != trust.Pass {
		t.Fatal("missing assessment authorized", r, err)
	}
}
