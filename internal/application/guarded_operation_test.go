package application_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

type authFunc func(context.Context, string) (authorization.Principal, error)

func (f authFunc) Authenticate(c context.Context, s string) (authorization.Principal, error) {
	return f(c, s)
}

type tenantFunc func(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error)

func (f tenantFunc) Resolve(c context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return f(c, p, s)
}

type entitlementFunc func(context.Context, string, feature.Key) (bool, error)

func (f entitlementFunc) Has(c context.Context, s string, k feature.Key) (bool, error) {
	return f(c, s, k)
}
func (f entitlementFunc) Limit(context.Context, string, feature.Key) (int64, error) {
	panic("unexpected quota bypass")
}
func (f entitlementFunc) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	panic("unexpected resolution")
}

type permissionFunc func(context.Context, authorization.Principal, string, authorization.Key) (bool, error)

func (f permissionFunc) Allowed(c context.Context, p authorization.Principal, s string, k authorization.Key) (bool, error) {
	return f(c, p, s, k)
}

type quotaFunc func(context.Context, application.Execution) error

func (f quotaFunc) Check(c context.Context, e application.Execution) error { return f(c, e) }

func TestGuardedOperationExactOrderAndFailureBoundaries(t *testing.T) {
	stages := []string{"authentication", "tenant resolution", "entitlement", "permission", "quota", "operation"}
	for _, stop := range append(slices.Clone(stages), "success", "entitlement denial", "permission denial", "tenant mismatch", "invalid principal") {
		for _, cancelAtStage := range []bool{false, true} {
			t.Run(stop+map[bool]string{false: " error", true: " cancel"}[cancelAtStage], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var calls []string
				private := errors.New("secret credential and backend data")
				check := func(stage string) error {
					calls = append(calls, stage)
					if stage == stop {
						if cancelAtStage {
							cancel()
							return nil
						}
						return private
					}
					return nil
				}
				principal := authorization.Principal{ID: "owner", Kind: authorization.User}
				target := tenant.Scope{OrganizationID: "org_a", ResourceID: "proof_1"}
				requirement := application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute}
				guard, err := application.NewGuardedOperation(
					authFunc(func(_ context.Context, credential string) (authorization.Principal, error) {
						if credential != "credential" {
							t.Fatal("credential changed")
						}
						err := check("authentication")
						p := principal
						if stop == "invalid principal" {
							p.Kind = "UNKNOWN"
						}
						return p, err
					}),
					tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
						if p != principal || s != target {
							t.Fatal("tenant inputs changed")
						}
						err := check("tenant resolution")
						if stop == "tenant mismatch" {
							s.OrganizationID = "org_b"
						}
						return s, err
					}),
					entitlementFunc(func(_ context.Context, org string, key feature.Key) (bool, error) {
						if org != target.OrganizationID || key != requirement.Feature {
							t.Fatal("entitlement inputs changed")
						}
						return stop != "entitlement denial", check("entitlement")
					}),
					permissionFunc(func(_ context.Context, p authorization.Principal, org string, key authorization.Key) (bool, error) {
						if p != principal || org != target.OrganizationID || key != requirement.Permission {
							t.Fatal("permission inputs changed")
						}
						return stop != "permission denial", check("permission")
					}),
					quotaFunc(func(_ context.Context, e application.Execution) error {
						if e.Actor != principal || e.Scope != target || e.Requirement != requirement {
							t.Fatal("quota inputs changed")
						}
						return check("quota")
					}),
					requirement, func(_ context.Context, e application.Execution) error {
						if e.Actor != principal || e.Scope != target || e.Requirement != requirement {
							t.Fatal("operation inputs changed")
						}
						return check("operation")
					})
				if err != nil {
					t.Fatal(err)
				}
				err = guard.Execute(ctx, application.Request{Credential: "credential", Target: target})
				last := stop
				switch stop {
				case "entitlement denial":
					last = "entitlement"
				case "permission denial":
					last = "permission"
				case "tenant mismatch":
					last = "tenant resolution"
				case "invalid principal":
					last = "authentication"
				}
				count := len(stages)
				if last != "success" {
					count = slices.Index(stages, last) + 1
				}
				if !slices.Equal(calls, stages[:count]) {
					t.Fatal("wrong stage order", calls)
				}
				if stop == "success" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil || strings.Contains(err.Error(), "secret") {
					t.Fatal("missing or leaky error", err)
				}
				if slices.Contains(stages, stop) {
					cause := private
					if cancelAtStage {
						cause = context.Canceled
					}
					if !errors.Is(err, cause) {
						t.Fatal("lost cause", err)
					}
				}
			})
		}
	}
}

func TestGuardCannotSkipRequiredStages(t *testing.T) {
	if _, err := application.NewGuardedOperation(nil, nil, nil, nil, nil, application.Requirement{}, nil); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	var zero application.GuardedOperation
	if err := zero.Execute(context.Background(), application.Request{}); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
}

func TestGuardRejectsRequestsBeforeAuthentication(t *testing.T) {
	calls := 0
	guard, err := application.NewGuardedOperation(
		authFunc(func(context.Context, string) (authorization.Principal, error) {
			calls++
			return authorization.Principal{}, nil
		}),
		tenantFunc(func(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error) {
			panic("unexpected tenant resolution")
		}),
		entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { panic("unexpected entitlement") }),
		permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
			panic("unexpected permission")
		}),
		quotaFunc(func(context.Context, application.Execution) error { panic("unexpected quota") }),
		application.Requirement{Feature: feature.KeyProofPlanner, Permission: authorization.KeyProofExecute},
		func(context.Context, application.Execution) error { panic("unexpected execution") })
	if err != nil {
		t.Fatal(err)
	}
	target := tenant.Scope{OrganizationID: "org_a", ResourceID: "proof_1"}
	for _, request := range []application.Request{{Target: target}, {Credential: strings.Repeat("x", 16385), Target: target}, {Credential: "secret", Target: tenant.Scope{OrganizationID: "org_a", ResourceID: "../proof"}}} {
		if err := guard.Execute(context.Background(), request); !errors.Is(err, application.ErrInvalidOperationRequest) {
			t.Fatal(err)
		}
	}
	if err := guard.Execute(nil, application.Request{Credential: "secret", Target: target}); !errors.Is(err, application.ErrInvalidOperationRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := guard.Execute(ctx, application.Request{Credential: "secret", Target: target}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("invalid request reached authentication")
	}
}
