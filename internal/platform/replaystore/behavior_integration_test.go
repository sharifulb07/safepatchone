package replaystore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	behavior "safepatch/internal/runtime"
)

type behaviorGrants struct{ *grants }

func (g behaviorGrants) Has(_ context.Context, org string, k feature.Key) (bool, error) {
	return g.enabled && org == "org" && (k == feature.KeyRuntimeBehaviorDNA || k == feature.KeyBusinessInvariants), nil
}
func (g behaviorGrants) Allowed(_ context.Context, a authorization.Principal, org string, k authorization.Key) (bool, error) {
	return g.allowed && a.ID == "owner" && org == "org" && (k == authorization.KeyReplayView || k == authorization.KeyProofExecute || k == authorization.KeyPolicyManage), nil
}

func TestBehaviorUsesReplayRetentionAndAuthenticDataset(t *testing.T) {
	f := setup(t)
	grants := behaviorGrants{f.grants}
	service, err := application.NewBehaviorService(tenants{}, grants, grants, grants, f.service, []behavior.Metric{{Name: "value", Source: "OUTPUT", Pointer: "/value"}}, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	d, err := service.Describe(t.Context(), f.actor, f.scope, "data")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := behavior.NewInvariantPolicy(behavior.InvariantFields{Schema: "safepatch.business-invariants.v1", Scope: f.scope, ID: "value", Version: "1", Rules: []behavior.BusinessInvariant{{ID: "positive", Terms: []behavior.InvariantTerm{{Metric: "value", Coefficient: "1"}}, Operator: "GE", Bound: "0"}}})
	result, err := service.Evaluate(t.Context(), f.actor, f.scope, d, p)
	if err != nil || result.Decision != behavior.InvariantSatisfied {
		t.Fatal(result, err)
	}
	raw, _ := json.Marshal(d)
	modified := bytes.Replace(raw, []byte(`"values":{"value":"2"}`), []byte(`"values":{"value":"999"}`), 1)
	forged, err := behavior.DecodeBehaviorDNA(modified)
	if err != nil || forged.Digest() == d.Digest() {
		t.Fatal("bad fixture", err)
	}
	if r, err := service.Evaluate(t.Context(), f.actor, f.scope, forged, p); err == nil || r.Decision != behavior.InvariantUnknown {
		t.Fatal("forged profile accepted", r, err)
	}
	before, _ := json.Marshal(f.dataset)
	f.now = f.now.Add(2 * 24 * time.Hour)
	if err := f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitDatasetRetentionDays, State: limits.Integer, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if r, err := service.Evaluate(t.Context(), f.actor, f.scope, d, p); err == nil || r.Decision != behavior.InvariantUnknown {
		t.Fatal("current retention ignored", r, err)
	}
	after, _ := json.Marshal(f.dataset)
	if !bytes.Equal(before, after) {
		t.Fatal("downgrade corrupted source")
	}
	if err := f.quota.SetLimit(t.Context(), "org", limits.EntitlementLimit{Key: limits.LimitDatasetRetentionDays, State: limits.Integer, Value: 7}); err != nil {
		t.Fatal(err)
	}
	if r, err := service.Evaluate(t.Context(), f.actor, f.scope, d, p); err != nil || r.Decision != behavior.InvariantSatisfied {
		t.Fatal("restored access failed", r, err)
	}
}
