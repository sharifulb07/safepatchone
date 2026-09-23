package proof_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/proof"
)

func fixture(t *testing.T) (*proof.ProofPlanner, proof.Request, []proof.Provider) {
	t.Helper()
	providers := []proof.Provider{}
	states := []proof.ProviderState{}
	for _, o := range proof.DefaultObligations() {
		providers = append(providers, proof.Provider{ID: o.ID, Version: "1.0.0", Capability: o.Capability, Costs: []proof.Cost{{Resource: "credits", Amount: 1}}})
		states = append(states, proof.ProviderState{ID: o.ID, Availability: proof.Available})
	}
	planner, err := proof.NewProofPlanner("1.0.0", proof.DefaultObligations(), providers)
	if err != nil {
		t.Fatal(err)
	}
	return planner, proof.Request{TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "commit1"}, Target: proof.L8, Providers: states, Budgets: []proof.Budget{{Resource: "credits", Remaining: 9}}}, providers
}
func fields(t *testing.T, p proof.Plan) proof.PlanFields {
	t.Helper()
	f, err := p.Fields()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProofLevelsCumulativeAndNeverAchievedByPlanning(t *testing.T) {
	planner, r, _ := fixture(t)
	names := []string{"syntax", "build", "tests", "security", "semantic", "behavioral", "adversarial", "runtime", "formal"}
	for i, l := range proof.Levels() {
		if l.Rank() != i || l.Name() != names[i] {
			t.Fatal(l)
		}
		r.Target = l
		p, err := planner.Plan(t.Context(), r)
		if err != nil {
			t.Fatal(err)
		}
		f := fields(t, p)
		if f.State != proof.Ready || len(f.Steps) != i+1 || len(f.Resolutions) != i+1 || f.Totals[0].Amount != int64(i+1) {
			t.Fatal(f)
		}
		for j, resolution := range f.Resolutions {
			if resolution.Code != proof.Selected || resolution.Obligation.Level.Rank() != j {
				t.Fatal(resolution)
			}
		}
		raw, _ := p.MarshalJSON()
		for _, forbidden := range []string{`"PASS"`, `"achieved_level"`, `"approved"`} {
			if bytes.Contains(raw, []byte(forbidden)) {
				t.Fatal("plan claimed achieved proof")
			}
		}
	}
	if proof.Level("L9").Rank() != -1 || proof.Level("").Name() != "" {
		t.Fatal("unknown level")
	}
}

func TestMissingUnknownDisabledAndOverBudgetDoNotDowngrade(t *testing.T) {
	planner, r, _ := fixture(t)
	for _, mode := range []string{"missing", "unknown", "disabled", "unavailable", "budget", "missing_budget"} {
		t.Run(mode, func(t *testing.T) {
			r := r
			r.Providers = slices.Clone(r.Providers)
			r.Budgets = []proof.Budget{{Resource: "credits", Remaining: 9}}
			want := proof.ProviderUnknown
			switch mode {
			case "missing":
				r.Providers = r.Providers[:8]
			case "unknown":
				r.Providers[8].Availability = proof.Unknown
			case "disabled":
				r.Providers[8].Availability = proof.Disabled
				want = proof.CapabilityDisabled
			case "unavailable":
				r.Providers[8].Availability = proof.Unavailable
				want = proof.ProviderUnavailable
			case "budget":
				r.Budgets[0].Remaining = 8
				want = proof.BudgetExceeded
			case "missing_budget":
				r.Budgets = nil
				want = proof.BudgetExceeded
			}
			p, err := planner.Plan(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			f := fields(t, p)
			if f.State != proof.Blocked || f.Target != proof.L8 || f.Resolutions[8].Code != want {
				t.Fatal(f)
			}
		})
	}
	empty, _ := proof.NewProofPlanner("1", proof.DefaultObligations(), nil)
	r.Providers = nil
	p, err := empty.Plan(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, p)
	if f.State != proof.Blocked || len(f.Steps) != 0 || len(f.Resolutions) != 9 {
		t.Fatal(f)
	}
	for _, r := range f.Resolutions {
		if r.Code != proof.ProviderUnavailable {
			t.Fatal(r)
		}
	}
}

func TestDeterministicPriorityFallbackReuseAndImmutableConfiguration(t *testing.T) {
	obligations := proof.DefaultObligations()
	obligations[1].Capability = obligations[0].Capability
	providers := []proof.Provider{
		{ID: "preferred", Version: "1", Capability: obligations[0].Capability, Priority: 0, Costs: []proof.Cost{{Resource: "credits", Amount: 10}}},
		{ID: "fallback", Version: "1", Capability: obligations[0].Capability, Priority: 1, Costs: []proof.Cost{{Resource: "credits", Amount: 2}}},
	}
	planner, err := proof.NewProofPlanner("1", obligations, providers)
	if err != nil {
		t.Fatal(err)
	}
	r := proof.Request{TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "r1"}, Target: proof.L1, Providers: []proof.ProviderState{{ID: "preferred", Availability: proof.Available}, {ID: "fallback", Availability: proof.Available}}, Budgets: []proof.Budget{{Resource: "credits", Remaining: 2}}}
	p, err := planner.Plan(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, p)
	if f.State != proof.Ready || len(f.Steps) != 1 || f.Steps[0].Provider.ID != "fallback" || f.Totals[0].Amount != 2 || f.Resolutions[0].StepID != f.Resolutions[1].StepID {
		t.Fatal(f)
	}
	slices.Reverse(obligations)
	slices.Reverse(providers)
	again, _ := proof.NewProofPlanner("1", obligations, providers)
	other, _ := again.Plan(t.Context(), r)
	if again.ConfigurationHash() != planner.ConfigurationHash() || other.Hash() != p.Hash() {
		t.Fatal("input order changed selection")
	}
	providers[0].Costs[0].Amount = 999
	obligations[0].ID = "mutated"
	exposed := planner.Providers()
	exposed[0].Costs[0].Amount = 999
	f.Steps[0].Provider.Costs[0].Amount = 999
	raw, _ := p.MarshalJSON()
	raw[0] = '!'
	after, _ := planner.Plan(t.Context(), r)
	if after.Hash() != p.Hash() {
		t.Fatal("caller mutated frozen plan/configuration")
	}
	if json.Unmarshal([]byte(`{}`), &p) == nil {
		t.Fatal("replaced sealed plan")
	}
	r.Subject.Revision = "r2"
	newRevision, _ := planner.Plan(t.Context(), r)
	if p.Hash() == newRevision.Hash() {
		t.Fatal("revision omitted from plan identity")
	}
}

func TestPlannerErrorsBoundsAndOverflow(t *testing.T) {
	planner, r, providers := fixture(t)
	for _, mode := range []string{"tenant", "subject", "level", "negative_budget", "duplicate_budget", "duplicate_state", "alien_state", "invalid_state", "oversize_states"} {
		t.Run(mode, func(t *testing.T) {
			bad := r
			bad.Providers = slices.Clone(r.Providers)
			bad.Budgets = slices.Clone(r.Budgets)
			switch mode {
			case "tenant":
				bad.TenantID = "../org"
			case "subject":
				bad.Subject.Revision = "../../ref"
			case "level":
				bad.Target = "L9"
			case "negative_budget":
				bad.Budgets[0].Remaining = -1
			case "duplicate_budget":
				bad.Budgets = append(bad.Budgets, bad.Budgets[0])
			case "duplicate_state":
				bad.Providers = append(bad.Providers, bad.Providers[0])
			case "alien_state":
				bad.Providers[0].ID = "alien"
			case "invalid_state":
				bad.Providers[0].Availability = "PASS"
			case "oversize_states":
				bad.Providers = make([]proof.ProviderState, proof.MaxProviders+1)
			}
			if _, err := planner.Plan(t.Context(), bad); !errors.Is(err, proof.ErrInvalidRequest) {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"version", "missing_level", "duplicate_obligation", "invalid_level", "bad_capability", "duplicate_provider", "zero_cost", "duplicate_cost", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			o := proof.DefaultObligations()
			p := planner.Providers()
			version := "1"
			switch mode {
			case "version":
				version = "bad\n"
			case "missing_level":
				o = o[:8]
			case "duplicate_obligation":
				o[1].ID = o[0].ID
			case "invalid_level":
				o[0].Level = "L9"
			case "bad_capability":
				p[0].Capability = "exec; rm"
			case "duplicate_provider":
				p = append(p, p[0])
			case "zero_cost":
				p[0].Costs[0].Amount = 0
			case "duplicate_cost":
				p[0].Costs = append(p[0].Costs, p[0].Costs[0])
			case "too_many":
				p = make([]proof.Provider, proof.MaxProviders+1)
			}
			if _, err := proof.NewProofPlanner(version, o, p); !errors.Is(err, proof.ErrInvalidPlanner) {
				t.Fatal(err)
			}
		})
	}
	providers[0].Costs[0].Amount = math.MaxInt64
	planner, _ = proof.NewProofPlanner("1", proof.DefaultObligations(), providers)
	r.Budgets[0].Remaining = math.MaxInt64
	p, err := planner.Plan(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	f := fields(t, p)
	if f.State != proof.Blocked || f.Totals[0].Amount != math.MaxInt64 {
		t.Fatal("overflow", f)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := planner.Plan(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := planner.Plan(nil, r); err == nil {
		t.Fatal("nil context")
	}
	var zero *proof.ProofPlanner
	if _, err := zero.Plan(t.Context(), r); err == nil {
		t.Fatal("nil planner")
	}
}

func TestPlannerConcurrentUseAndUntrustedIdentifiers(t *testing.T) {
	planner, r, _ := fixture(t)
	baseline, _ := planner.Plan(t.Context(), r)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			for j := 0; j < 10; j++ {
				p, err := planner.Plan(t.Context(), r)
				if err != nil || p.Hash() != baseline.Hash() {
					t.Error("unstable shared planner", err)
				}
				f, _ := p.Fields()
				f.Steps[0].Provider.Costs[0].Amount = 99
			}
		})
	}
	wg.Wait()
	providers := planner.Providers()
	providers[0].ID = strings.Repeat("a", 128)
	long, _ := proof.NewProofPlanner("1", proof.DefaultObligations(), providers)
	r.Providers = nil
	for _, v := range long.Providers() {
		r.Providers = append(r.Providers, proof.ProviderState{ID: v.ID, Availability: proof.Available})
	}
	p, err := long.Plan(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range fields(t, p).Steps {
		if !evidence.ValidIdentifier(s.ID) {
			t.Fatal("invalid generated step ID")
		}
	}
}
