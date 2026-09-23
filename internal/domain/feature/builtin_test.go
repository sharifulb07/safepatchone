package feature_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"safepatch/internal/domain/feature"
)

// Pin published identities independently of catalog iteration order.
func TestBuiltinIdentityContract(t *testing.T) {
	expected := map[string]feature.Key{
		"feat_0046": "proof.quorum",
		"feat_0001": "verify.basic",
		"feat_0002": "verify.security",
		"feat_0003": "verify.api",
		"feat_0004": "verify.polyglot",
		"feat_0005": "intent.basic",
		"feat_0006": "intent.advanced",
		"feat_0007": "intent.specforge",
		"feat_0008": "graph.local",
		"feat_0009": "graph.organization",
		"feat_0010": "graph.cross_repo",
		"feat_0011": "proof.planner",
		"feat_0012": "proof.passport",
		"feat_0013": "proof.living",
		"feat_0014": "testtrust",
		"feat_0015": "runtime.replay",
		"feat_0016": "runtime.behaviordna",
		"feat_0017": "runtime.shadow",
		"feat_0018": "business.invariants",
		"feat_0019": "incident.antibodies",
		"feat_0020": "agent.provenance",
		"feat_0021": "agent.mcp_inventory",
		"feat_0022": "agent.mcp_gateway",
		"feat_0023": "agent.safelease",
		"feat_0024": "agent.temporalguard",
		"feat_0025": "enterprise.sso",
		"feat_0026": "enterprise.audit",
		"feat_0027": "enterprise.self_hosted",
		"feat_0028": "enterprise.airgap",
		"feat_0029": "enterprise.lts",
		"feat_0030": "proof.cloud_history",
		"feat_0031": "integration.github.private",
		"feat_0032": "language.additional_packs",
		"feat_0033": "platform.organizations",
		"feat_0034": "policy.central",
		"feat_0035": "proof.shared",
		"feat_0036": "audit.organization",
		"feat_0037": "verify.pr_enforcement",
		"feat_0038": "verify.composition",
		"feat_0039": "evidence.local",
		"feat_0040": "policy.local.basic",
		"feat_0041": "policy.local.advanced",
		"feat_0042": "proof.composition",
		"feat_0043": "graph.global_change",
		"feat_0044": "automation.safe_upgrade",
		"feat_0045": "agent.earned_autonomy",
	}
	registered := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	catalog, err := feature.NewBuiltinCatalog("0.1.0", registered)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Len() != len(expected) {
		t.Fatalf("got %d features, want %d", catalog.Len(), len(expected))
	}
	for id, key := range expected {
		got, err := catalog.GetByID(id)
		if err != nil || got.Key != key {
			t.Fatalf("identity %s: %+v, %v", id, got, err)
		}
		if got.Status != feature.StatusPlanned || got.IntroducedVersion != "0.1.0" || got.DeprecatedVersion != "" || !got.CreatedAt.Equal(registered) {
			t.Fatalf("unexpected lifecycle for %s: %+v", key, got)
		}
		if got.DefaultLimitType != feature.LimitNone || len(got.RequiresFeatures) != 0 {
			t.Fatalf("invented commercial policy for %s", key)
		}
	}
	// A catalog describes capabilities; it has no plan, organization, or actor
	// grants. Assert the metadata wire contract rather than simulate access checks.
	data, err := json.Marshal(catalog.List())
	if err != nil {
		t.Fatal(err)
	}
	var objects []map[string]json.RawMessage
	if err := json.Unmarshal(data, &objects); err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if len(object) != 11 {
			t.Fatalf("unexpected feature fields: %v", object)
		}
	}
}

func TestBuiltinConstructionIsReproducibleAndIndependent(t *testing.T) {
	registered := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	first, err := feature.NewBuiltinCatalog("1.0.0-rc.1", registered)
	if err != nil {
		t.Fatal(err)
	}
	second, err := feature.NewBuiltinCatalog("1.0.0-rc.1", registered)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first.List())
	b, _ := json.Marshal(second.List())
	if string(a) != string(b) {
		t.Fatal("catalog is nondeterministic")
	}
	first.List()[0].Name = "changed"
	again, _ := json.Marshal(second.List())
	if string(again) != string(b) {
		t.Fatal("shared mutable seed state")
	}
	for _, tc := range []struct {
		version string
		at      time.Time
	}{{"Team", registered}, {"1.0.0", time.Time{}}} {
		catalog, err := feature.NewBuiltinCatalog(tc.version, tc.at)
		if catalog != nil || !errors.Is(err, feature.ErrInvalidFeature) {
			t.Fatalf("invalid seed metadata accepted: %v", err)
		}
	}
}
