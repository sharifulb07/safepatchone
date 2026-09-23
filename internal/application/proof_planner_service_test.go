package application_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/tenant"
)

type proofSourceFunc func(context.Context, tenant.Scope, evidence.Subject, proof.Provider) (proof.Availability, error)

func (f proofSourceFunc) Check(ctx context.Context, s tenant.Scope, subject evidence.Subject, p proof.Provider) (proof.Availability, error) {
	return f(ctx, s, subject, p)
}

// Mutation methods are intentionally absent through nil embedding. Planning must
// never create/settle reservations or modify usage while estimating a plan.
type plannerQuotaFixture struct {
	t      *testing.T
	limit  domain.EntitlementLimit
	usage  entitlement.Usage
	err    error
	calls  int
	period string
}

func (s *plannerQuotaFixture) WithinOrganization(_ context.Context, org string, f func(entitlement.QuotaTransaction) error) error {
	if org != "org" {
		s.t.Fatal("cross-tenant quota lookup")
	}
	s.calls++
	if s.err != nil {
		return s.err
	}
	return f(plannerQuotaTx{s: s})
}

type plannerQuotaTx struct {
	entitlement.QuotaTransaction
	s *plannerQuotaFixture
}

func (tx plannerQuotaTx) Limit(k domain.LimitKey) (domain.EntitlementLimit, error) {
	if k != domain.LimitProofCreditsMonthly {
		tx.s.t.Fatal("wrong quota resource", k)
	}
	return tx.s.limit, nil
}
func (tx plannerQuotaTx) Usage(k domain.LimitKey, period string) (entitlement.Usage, error) {
	if k != domain.LimitProofCreditsMonthly || period != "2026-09" {
		tx.s.t.Fatal("wrong quota period/resource", period, k)
	}
	tx.s.period = period
	return tx.s.usage, nil
}

type plannerServiceFixture struct {
	service                            *application.ProofPlannerService
	quotas                             *plannerQuotaFixture
	grants                             map[feature.Key]bool
	permission                         map[authorization.Key]bool
	sourceCalls                        int
	state                              proof.Availability
	sourceErr, grantErr, permissionErr error
	foreign                            bool
	request                            application.ProofPlanningRequest
	actor                              authorization.Principal
	scope                              tenant.Scope
}

func newPlannerServiceFixture(t *testing.T) *plannerServiceFixture {
	t.Helper()
	f := &plannerServiceFixture{grants: map[feature.Key]bool{feature.KeyProofPlanner: true, feature.KeyVerifyBasic: true, feature.KeyGraphOrganization: true, feature.KeyIntentAdvanced: true}, permission: map[authorization.Key]bool{authorization.KeyProofExecute: true, authorization.KeyProofView: true}, state: proof.Available, actor: authorization.Principal{ID: "actor", Kind: authorization.ServiceAccount}, scope: tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, request: application.ProofPlanningRequest{Subject: evidence.Subject{RepositoryID: "repo", Revision: "commit1"}, Target: proof.L0}}
	f.quotas = &plannerQuotaFixture{t: t, limit: domain.EntitlementLimit{Key: domain.LimitProofCreditsMonthly, State: domain.Integer, Value: 10}, usage: entitlement.Usage{Reserved: 2, Committed: 3}}
	// A trusted test provider with explicitly configured organization intelligence
	// and advanced-provider requirements, rather than a new commercial branch.
	planner, err := proof.NewProofPlanner("1", proof.DefaultObligations(), []proof.Provider{{ID: "syntax", Version: "1", Capability: "proof.syntax", Costs: []proof.Cost{{Resource: string(domain.LimitProofCreditsMonthly), Amount: 5}}}, {ID: "future", Version: "1", Capability: "proof.formal"}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	f.service, err = application.NewProofPlannerService(planner, catalog, tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		if p != f.actor || s != f.scope {
			return tenant.Scope{}, tenant.ErrInvalidScope
		}
		if f.foreign {
			s.OrganizationID = "foreign"
		}
		return s, nil
	}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
		if org != "org" {
			t.Fatal("tenant lost")
		}
		return f.grants[k], f.grantErr
	}), permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
		if p != f.actor || org != "org" {
			t.Fatal("actor lost")
		}
		return f.permission[k], f.permissionErr
	}), quotaFunc(func(_ context.Context, e application.Execution) error {
		if e.Requirement.Feature != feature.KeyProofPlanner {
			t.Fatal("wrong planning quota")
		}
		return nil
	}), f.quotas, proofSourceFunc(func(_ context.Context, s tenant.Scope, subject evidence.Subject, p proof.Provider) (proof.Availability, error) {
		f.sourceCalls++
		if s != f.scope || subject != f.request.Subject || p.ID != "syntax" {
			t.Fatal("probed irrelevant/foreign provider", p.ID)
		}
		p.Costs[0].Resource = "tampered"
		p.Costs[0].Amount = 0
		return f.state, f.sourceErr
	}), []application.ProofProviderBinding{{ProviderID: "syntax", Features: []feature.Key{feature.KeyVerifyBasic, feature.KeyGraphOrganization, feature.KeyIntentAdvanced}, Permission: authorization.KeyProofView}, {ProviderID: "future", Features: []feature.Key{feature.KeyVerifyBasic}, Permission: authorization.KeyProofExecute}}, func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *plannerServiceFixture) plan(t *testing.T) (proof.Plan, error) {
	t.Helper()
	return f.service.Plan(t.Context(), f.actor, f.scope, f.request)
}
func planFields(t *testing.T, p proof.Plan) proof.PlanFields {
	t.Helper()
	f, err := p.Fields()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestProofPlannerEditionIndependentAdmissionAndRestoration(t *testing.T) {
	for _, edition := range edition.Editions() {
		t.Run(edition.Name, func(t *testing.T) {
			f := newPlannerServiceFixture(t)
			initial, err := f.plan(t)
			if err != nil || planFields(t, initial).State != proof.Ready {
				t.Fatal(err)
			}
			hash := initial.Hash()
			before := f.sourceCalls
			f.grants[feature.KeyProofPlanner] = false
			if _, err := f.plan(t); !errors.Is(err, entitlement.ErrNotEntitled) {
				t.Fatal("planner access bypass", err)
			}
			f.grants[feature.KeyProofPlanner] = true
			f.permission[authorization.KeyProofExecute] = false
			if _, err := f.plan(t); !errors.Is(err, application.ErrPermissionDenied) {
				t.Fatal("planner permission bypass", err)
			}
			f.permission[authorization.KeyProofExecute] = true
			if f.sourceCalls != before {
				t.Fatal("denied planner probed providers")
			}
			for _, key := range []feature.Key{feature.KeyVerifyBasic, feature.KeyGraphOrganization, feature.KeyIntentAdvanced} {
				f.grants[key] = false
				blocked, err := f.plan(t)
				if err != nil {
					t.Fatal(err)
				}
				fields := planFields(t, blocked)
				if fields.State != proof.Blocked || fields.Resolutions[0].Code != proof.CapabilityDisabled {
					t.Fatal("provider grant bypass", fields)
				}
				f.grants[key] = true
			}
			f.permission[authorization.KeyProofView] = false
			blocked, err := f.plan(t)
			if err != nil || planFields(t, blocked).State != proof.Blocked {
				t.Fatal("provider permission bypass", err)
			}
			f.permission[authorization.KeyProofView] = true
			if f.sourceCalls != before {
				t.Fatal("unauthorized intelligence provider called")
			}
			f.quotas.limit.Value = 9
			blocked, err = f.plan(t)
			if err != nil || planFields(t, blocked).Resolutions[0].Code != proof.BudgetExceeded {
				t.Fatal("reserved usage ignored", err)
			}
			f.quotas.limit.Value = 10
			restored, err := f.plan(t)
			if err != nil || restored.Hash() != hash || initial.Hash() != hash {
				t.Fatal("plan change altered immutable work", err)
			}
			if f.quotas.usage != (entitlement.Usage{Reserved: 2, Committed: 3}) {
				t.Fatal("planning mutated accounting")
			}
		})
	}
}

func TestProofPlannerReadinessQuotaSecurityAndErrorPaths(t *testing.T) {
	for _, mode := range []string{"unknown", "unavailable", "invalid_readiness", "source_error", "grant_error", "permission_error", "budget_error", "disabled_limit", "unlimited", "missing_limit", "overflow", "negative_usage", "overdrawn", "foreign", "other_repo", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			f := newPlannerServiceFixture(t)
			private := errors.New("credential://private")
			wantError := false
			switch mode {
			case "unknown":
				f.state = proof.Unknown
			case "unavailable":
				f.state = proof.Unavailable
			case "invalid_readiness":
				f.state = "PASS"
				wantError = true
			case "source_error":
				f.sourceErr = private
				wantError = true
			case "grant_error":
				f.grantErr = private
				wantError = true
			case "permission_error":
				f.permissionErr = private
				wantError = true
			case "budget_error":
				f.quotas.err = private
				wantError = true
			case "disabled_limit":
				f.quotas.limit.State = domain.Disabled
				f.quotas.limit.Value = 0
			case "unlimited":
				f.quotas.limit.State = domain.Unlimited
				f.quotas.limit.Value = 0
			case "missing_limit":
				f.quotas.limit = domain.EntitlementLimit{}
				wantError = true
			case "overflow":
				f.quotas.usage = entitlement.Usage{Reserved: math.MaxInt64, Committed: 1}
				wantError = true
			case "negative_usage":
				f.quotas.usage.Reserved = -1
				wantError = true
			case "overdrawn":
				f.quotas.limit.Value = 1
			case "foreign":
				f.foreign = true
				wantError = true
			case "other_repo":
				f.request.Subject.RepositoryID = "other"
				wantError = true
			case "cancel":
				wantError = true
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			p, err := f.service.Plan(ctx, f.actor, f.scope, f.request)
			if (err != nil) != wantError {
				t.Fatal(mode, err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "credential") || p.Hash() != "" {
					t.Fatal("failure exposed provider secret or partial plan")
				}
				return
			}
			fields := planFields(t, p)
			if mode == "unlimited" {
				if fields.State != proof.Ready {
					t.Fatal(fields)
				}
			} else if fields.State != proof.Blocked {
				t.Fatal("unknown/disabled admitted", fields)
			}
		})
	}
}

func TestProofPlannerServiceRejectsMissingBindingsAndReusableCosts(t *testing.T) {
	f := newPlannerServiceFixture(t)
	catalog, _ := feature.NewBuiltinCatalog("1", time.Now())
	for _, mode := range []string{"binding", "duplicate", "unknown_feature", "no_feature", "capacity"} {
		provider := proof.Provider{ID: "syntax", Version: "1", Capability: "proof.syntax"}
		if mode == "capacity" {
			provider.Costs = []proof.Cost{{Resource: string(domain.LimitVerificationConcurrency), Amount: 1}}
		}
		planner, _ := proof.NewProofPlanner("1", proof.DefaultObligations(), []proof.Provider{provider})
		bindings := []application.ProofProviderBinding{{ProviderID: "syntax", Features: []feature.Key{feature.KeyVerifyBasic}, Permission: authorization.KeyProofExecute}}
		switch mode {
		case "binding":
			bindings = nil
		case "duplicate":
			bindings[0].Features = append(bindings[0].Features, feature.KeyVerifyBasic)
		case "unknown_feature":
			bindings[0].Features = []feature.Key{"unknown.feature"}
		case "no_feature":
			bindings[0].Features = nil
		}
		_, err := application.NewProofPlannerService(planner, catalog, tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
			return s, nil
		}), entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return true, nil }), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
			return true, nil
		}), quotaFunc(func(context.Context, application.Execution) error { return nil }), f.quotas, proofSourceFunc(func(context.Context, tenant.Scope, evidence.Subject, proof.Provider) (proof.Availability, error) {
			return proof.Available, nil
		}), bindings, time.Now)
		if err == nil {
			t.Fatal("invalid provider configuration", mode)
		}
	}
	var nilService *application.ProofPlannerService
	if _, err := nilService.Plan(t.Context(), f.actor, f.scope, f.request); err == nil {
		t.Fatal("nil service")
	}
}
