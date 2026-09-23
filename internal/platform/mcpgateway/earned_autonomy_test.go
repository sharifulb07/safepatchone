package mcpgateway_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/agent"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/config"
	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/platform/agentstore"
	"safepatch/internal/platform/mcpgateway"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

type autonomyAccess struct {
	*access
	limit   int64
	approve bool
}

func (a *autonomyAccess) Limit(context.Context, string, feature.Key) (int64, error) {
	return a.limit, nil
}
func (a *autonomyAccess) Allowed(ctx context.Context, p authorization.Principal, org string, key authorization.Key) (bool, error) {
	if key == authorization.KeyAgentApprove {
		return a.approve && a.allowed && p.ID == "owner" && org == "org", nil
	}
	return a.access.Allowed(ctx, p, org, key)
}

type autonomySource struct {
	policy  agent.AutonomyPolicy
	records []agent.AutonomyProof
	reads   int
	fail    bool
	hook    func()
}

func (s *autonomySource) AutonomyPolicy(context.Context, agent.Scope, string) (agent.AutonomyPolicy, error) {
	s.reads++
	if s.fail {
		return agent.AutonomyPolicy{}, errors.New("private secret")
	}
	return s.policy, nil
}
func (s *autonomySource) AutonomyProofs(context.Context, authorization.Principal, agent.Scope, string) ([]agent.AutonomyProof, error) {
	if s.hook != nil {
		s.hook()
	}
	if s.fail {
		return nil, errors.New("private secret")
	}
	return s.records, nil
}
func autonomyFixture(t testing.TB) (*fixture, *application.EarnedAutonomyService, *autonomyAccess, *autonomySource) {
	t.Helper()
	f := setup(t)
	a := &autonomyAccess{access: f.access, limit: 32, approve: true}
	a.enabled[feature.KeyAgentEarnedAutonomy] = true
	runtime, err := mcpgateway.NewRuntime([]mcpgateway.Binding{{Scope: f.scope, ServerID: "server", Client: f.rpc}})
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = application.NewAgentGateway(tenants{f.access}, a, a, a, policySource{f}, runtime, f.store, func() time.Time { return f.now }, agentstore.NewID)
	if err != nil {
		t.Fatal(err)
	}
	source := &autonomySource{policy: agent.AutonomyPolicy{ID: "operator_policy", Policy: f.policy, MinimumProofs: 1, MinimumLevel: proof.L2, MaxProofAgeSeconds: 3600}}
	// Operator-reviewed escalation permits two calls; ordinary issuance permits one.
	f.policy.CallBudget = 1
	service, err := application.NewEarnedAutonomyService(f.service, source)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := service.Challenge(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := runtime.Inventory(t.Context(), f.scope)
	if err != nil {
		t.Fatal(err)
	}
	caps, err := agent.Compile(f.contract, inv, source.policy.Policy, []agent.Call{f.call})
	if err != nil {
		t.Fatal(err)
	}
	cf, _ := caps.Fields()
	change := domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	hash := evidence.Digest([]byte("verified"))
	pp, err := passport.New(passport.Fields{Change: change, ChangeID: change.ID(), RunID: "verified_run", IntentDigest: cf.IntentDigest, Commit: change.HeadSHA, ProofLevel: proof.L2, ProofPlanDigest: hash, PolicyDecision: passport.PolicyDecision{PolicyDigest: hash, ResultDigest: hash, Status: evidence.StatusPass}, EvidenceGraphDigest: hash, VerificationDecision: trust.Pass, IssuedAt: f.now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	source.records = []agent.AutonomyProof{{Actor: f.actor, ChallengeDigest: challenge, Passport: pp, ValidUntil: f.now.Add(time.Hour)}}
	source.reads = 0
	return f, service, a, source
}
func TestEarnedAutonomyAllEditionsAndHistory(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Now().UTC())
	matrix, err := config.EarnedAutonomyEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range matrix.Editions {
		t.Run(string(entry.Bundle.Version.EditionKey), func(t *testing.T) {
			f, s, a, source := autonomyFixture(t)
			a.enabled = map[feature.Key]bool{}
			for _, row := range entry.Bundle.Features {
				ft, err := fc.GetByID(row.FeatureID)
				if err != nil {
					t.Fatal(err)
				}
				a.enabled[ft.Key] = row.Enabled
				if ft.Key == feature.KeyAgentEarnedAutonomy && row.LimitValue != nil {
					a.limit = *row.LimitValue
				}
			}
			l, err := s.Issue(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
			if !a.enabled[feature.KeyAgentEarnedAutonomy] {
				if err == nil || source.reads != 0 || f.rpc.calls.Load() != 0 {
					t.Fatal("unentitled escalation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Invoke(t.Context(), f.actor, f.scope, l.ID, "bypass", f.call); err == nil {
				t.Fatal("normal path bypass")
			}
			invoke := func(id string) error {
				_, err := s.Invoke(t.Context(), f.actor, f.scope, l.ID, id, f.contract, []agent.Call{f.call}, f.call)
				return err
			}
			a.approve = false
			if invoke("no_approval") == nil {
				t.Fatal("entitlement replaced authorization")
			}
			a.approve = true
			a.enabled[feature.KeyAgentEarnedAutonomy] = false
			if invoke("downgraded") == nil {
				t.Fatal("downgrade access")
			}
			saved, err := f.store.Load(t.Context(), f.scope, l.ID)
			if err != nil || saved.Capabilities.Digest() != l.Capabilities.Digest() || saved.Uses != 0 || f.rpc.calls.Load() != 0 {
				t.Fatal("history changed")
			}
			a.enabled[feature.KeyAgentEarnedAutonomy] = true
			if err := invoke("one"); err != nil {
				t.Fatal(err)
			}
			if invoke("one") == nil {
				t.Fatal("replayed invocation")
			}
			if err := invoke("two"); err != nil {
				t.Fatal(err)
			}
			if invoke("three") == nil || f.rpc.calls.Load() != 2 {
				t.Fatal("budget bypass")
			}
			a.enabled[feature.KeyAgentEarnedAutonomy] = false
			if err := f.service.Revoke(t.Context(), f.actor, f.scope, l.ID); err != nil {
				t.Fatal("revocation blocked after downgrade", err)
			}
		})
	}
}
func TestEarnedAutonomyDriftAndErrorPaths(t *testing.T) {
	for _, mode := range []string{"proof_removed", "policy", "inventory", "limit", "permission", "quota", "expiry", "proof_shortened", "tenant", "actor", "error", "changed_during_read", "normal_lease"} {
		t.Run(mode, func(t *testing.T) {
			f, s, a, source := autonomyFixture(t)
			l, err := s.Issue(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
			if err != nil {
				t.Fatal(err)
			}
			scope, actor := f.scope, f.actor
			switch mode {
			case "proof_removed":
				source.records = nil
			case "policy":
				source.policy.Policy.Revision++
			case "inventory":
				f.rpc.definition = []byte(`{"name":"read","description":"changed","inputSchema":{"type":"object"}}`)
			case "limit":
				a.limit = 1
			case "permission":
				a.approve = false
			case "quota":
				a.quota = false
			case "expiry":
				f.now = l.ExpiresAt
			case "proof_shortened":
				source.records[0].ValidUntil = f.now.Add(time.Second)
			case "tenant":
				scope.OrganizationID = "other"
			case "actor":
				actor.ID = "other"
			case "error":
				source.fail = true
			case "changed_during_read":
				source.hook = func() { a.enabled[feature.KeyAgentEarnedAutonomy] = false }
			case "normal_lease":
				l = issue(t, f)
			}
			result, err := s.Invoke(t.Context(), actor, scope, l.ID, "attempt", f.contract, []agent.Call{f.call}, f.call)
			if err == nil || strings.Contains(err.Error(), "private secret") || result.Receipt.Outcome != agent.OutcomeUnknown || f.rpc.calls.Load() != 0 {
				t.Fatal("unsafe dispatch", mode, err)
			}
			stored, err := f.store.Load(t.Context(), f.scope, l.ID)
			if err != nil || stored.Uses != 0 {
				t.Fatal("failed admission consumed lease")
			}
		})
	}
}
func TestEarnedAutonomyIssueFailsClosed(t *testing.T) {
	for _, mode := range []string{"short_proof", "no_proof", "no_approval", "zero_limit", "excess_limit", "proof_error", "changed_during_read", "wrong_policy"} {
		t.Run(mode, func(t *testing.T) {
			f, s, a, source := autonomyFixture(t)
			switch mode {
			case "short_proof":
				source.records[0].ValidUntil = f.now.Add(time.Second)
			case "no_proof":
				source.records = nil
			case "no_approval":
				a.approve = false
			case "zero_limit":
				a.limit = 0
			case "excess_limit":
				a.limit = 33
			case "proof_error":
				source.fail = true
			case "changed_during_read":
				source.hook = func() { a.limit = 1 }
			case "wrong_policy":
				source.policy.Policy.Scope.OrganizationID = "other"
			}
			l, err := s.Issue(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
			if err == nil || l.ID != "" || f.rpc.calls.Load() != 0 {
				t.Fatal("unsafe issuance", err)
			}
		})
	}
}

func TestEarnedAutonomyConfiguredLimitIntersectsPolicy(t *testing.T) {
	f, s, a, source := autonomyFixture(t)
	// A smaller commercially assigned limit changes no kernel or dispatcher code.
	a.limit = 1
	challenge, err := s.Challenge(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
	if err != nil {
		t.Fatal(err)
	}
	source.records[0].ChallengeDigest = challenge // trusted verifier fixture for new input
	l, err := s.Issue(t.Context(), f.actor, f.scope, source.policy.ID, f.contract, []agent.Call{f.call})
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := l.Capabilities.Fields()
	if fields.CallBudget != 1 || source.policy.Policy.CallBudget != 2 {
		t.Fatal("entitlement did not cap policy")
	}
	if _, err := s.Invoke(t.Context(), f.actor, f.scope, l.ID, "one", f.contract, []agent.Call{f.call}, f.call); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Invoke(t.Context(), f.actor, f.scope, l.ID, "two", f.contract, []agent.Call{f.call}, f.call); err == nil || f.rpc.calls.Load() != 1 {
		t.Fatal("configured limit bypassed")
	}
}
