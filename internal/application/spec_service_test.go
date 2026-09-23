package application_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

func specBindings(t *testing.T, c intent.IntentContract) []spec.Binding {
	t.Helper()
	a, err := spec.Assertions(c)
	if err != nil {
		t.Fatal(err)
	}
	return []spec.Binding{{AssertionID: a[0].ID, Evidence: policy.EvidenceRequirement{Producer: "test", Type: "intent.constraint"}}}
}

func TestSpecServiceFixedCapabilityAndAuthorizationBoundaries(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	c := fullIntent(t)
	raw, _ := c.CanonicalJSON()
	b := specBindings(t, c)
	actor := authorization.Principal{ID: "actor", Kind: authorization.ServiceAccount}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	for _, offering := range edition.Editions() {
		for _, key := range []feature.Key{feature.KeyIntentBasic, feature.KeyIntentAdvanced, feature.KeyIntentSpecforge} {
			t.Run(offering.Name+"/"+string(key), func(t *testing.T) {
				granted, permitted := true, true
				var calls []string
				service, err := application.NewSpecService(fc,
					tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
						calls = append(calls, "tenant")
						if p != actor || s != scope {
							return tenant.Scope{}, tenant.ErrInvalidScope
						}
						return s, nil
					}),
					entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
						calls = append(calls, "entitlement")
						if org != "org" || k != key {
							t.Fatal("operation chose wrong capability", k)
						}
						return granted, nil
					}),
					permissionFunc(func(_ context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
						calls = append(calls, "permission")
						if p != actor || org != "org" || k != authorization.KeyProofExecute {
							t.Fatal("wrong permission")
						}
						return permitted, nil
					}),
					quotaFunc(func(_ context.Context, e application.Execution) error {
						calls = append(calls, "quota")
						if e.Requirement.Feature != key || e.Scope != scope {
							t.Fatal("wrong quota")
						}
						return nil
					}))
				if err != nil {
					t.Fatal(err)
				}
				var artifact spec.Specification
				invoke := func() error {
					switch key {
					case feature.KeyIntentBasic:
						r, err := service.Validate(t.Context(), actor, scope, raw)
						if err != nil && r.Status != evidence.StatusError {
							t.Fatal("denied validation succeeded")
						}
						return err
					case feature.KeyIntentAdvanced:
						a, err := service.Analyze(t.Context(), actor, scope, c)
						if err != nil && a.ContractHash != "" {
							t.Fatal("denied analysis exposed output")
						}
						return err
					default:
						s, err := service.Generate(t.Context(), actor, scope, c, b)
						if err == nil {
							artifact = s
						} else if s.ContentHash() != "" {
							t.Fatal("denied generation exposed output")
						}
						return err
					}
				}
				if err := invoke(); err != nil || !slices.Equal(calls, []string{"tenant", "entitlement", "permission", "quota"}) {
					t.Fatal(err, calls)
				}
				hash := artifact.ContentHash()
				granted = false
				calls = nil
				if err := invoke(); !errors.Is(err, entitlement.ErrNotEntitled) || !slices.Equal(calls, []string{"tenant", "entitlement"}) {
					t.Fatal(err, calls)
				}
				granted, permitted = true, false
				calls = nil
				if err := invoke(); !errors.Is(err, application.ErrPermissionDenied) || slices.Contains(calls, "quota") {
					t.Fatal(err, calls)
				}
				permitted = true
				if err := invoke(); err != nil || artifact.ContentHash() != hash {
					t.Fatal("restored grant changed artifact", err)
				}
				after, _ := c.CanonicalJSON()
				if !bytes.Equal(raw, after) {
					t.Fatal("edition or grant change mutated contract")
				}
			})
		}
	}
}

func TestCommunitySpecDoctorIsOfflineAndAdvancedOperationsAreDenied(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	provider, _ := entitlement.NewCommunityEntitlementProvider()
	actor := authorization.Principal{ID: "local-user", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: entitlement.LocalOrganizationID, ResourceID: "repo"}
	allowed := true
	s, err := application.NewSpecService(fc, tenantFunc(func(_ context.Context, _ authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
		return scope, nil
	}), provider,
		permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
			return allowed, nil
		}), quotaFunc(func(context.Context, application.Execution) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	c := fullIntent(t)
	raw, _ := c.CanonicalJSON()
	if r, err := s.Validate(t.Context(), actor, scope, raw); err != nil || r.Status != evidence.StatusPass {
		t.Fatal(r, err)
	}
	if _, err := s.Analyze(t.Context(), actor, scope, c); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal(err)
	}
	if _, err := s.Generate(t.Context(), actor, scope, c, specBindings(t, c)); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal(err)
	}
	allowed = false
	if _, err := s.Validate(t.Context(), actor, scope, raw); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal(err)
	}
}

func TestSpecServiceFailsClosedOnInfrastructureAndInputErrors(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	c := fullIntent(t)
	secret := errors.New("secret connection credential")
	for _, scenario := range []string{"tenant_error", "foreign_scope", "entitlement_error", "permission_error", "quota_error", "cancel", "nil_context", "bad_contract"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := application.NewSpecService(fc,
				tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if scenario == "tenant_error" {
						return s, secret
					}
					if scenario == "foreign_scope" {
						s.OrganizationID = "other"
					}
					return s, nil
				}),
				entitlementFunc(func(context.Context, string, feature.Key) (bool, error) {
					if scenario == "entitlement_error" {
						return true, secret
					}
					return true, nil
				}),
				permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
					if scenario == "permission_error" {
						return true, secret
					}
					return true, nil
				}),
				quotaFunc(func(context.Context, application.Execution) error {
					if scenario == "quota_error" {
						return secret
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			var ctx context.Context = t.Context()
			if scenario == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if scenario == "nil_context" {
				ctx = nil
			}
			input := c
			if scenario == "bad_contract" {
				input = intent.IntentContract{}
			}
			generated, err := s.Generate(ctx, actor, scope, input, specBindings(t, c))
			if err == nil || generated.ContentHash() != "" || strings.Contains(err.Error(), secret.Error()) {
				t.Fatal(generated, err)
			}
		})
	}
	if _, err := application.NewSpecService(nil, nil, nil, nil, nil); err == nil {
		t.Fatal("nil catalog")
	}
	if _, err := application.NewSpecService(fc, nil, nil, nil, nil); err == nil {
		t.Fatal("nil dependencies")
	}
	for _, s := range []*application.SpecService{nil, {}} {
		if _, err := s.Validate(t.Context(), actor, scope, nil); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
		if _, err := s.Analyze(t.Context(), actor, scope, c); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
		if _, err := s.Generate(t.Context(), actor, scope, c, nil); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
	}
}

func TestSpecWorkerRechecksBeforeRealGeneration(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	r, _ := application.IntentRequirement(fc, feature.KeyIntentSpecforge)
	c := fullIntent(t)
	b := specBindings(t, c)
	actor := authorization.Principal{ID: "worker", Kind: authorization.ServiceAccount}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	granted, allowed := true, true
	runs := 0
	var artifact spec.Specification
	w, err := application.NewWorkerOperation(workloadRepo(func(_ context.Context, org, id string) (application.Workload, error) {
		if org != "org" || id != "job" {
			return application.Workload{}, application.ErrWorkloadNotFound
		}
		return application.Workload{ID: id, Kind: "specforge", Actor: actor, Target: scope}, nil
	}), tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}),
		entitlementFunc(func(_ context.Context, _ string, k feature.Key) (bool, error) {
			if k != feature.KeyIntentSpecforge {
				t.Fatal(k)
			}
			return granted, nil
		}),
		permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
			return allowed, nil
		}), quotaFunc(func(context.Context, application.Execution) error { return nil }), "specforge", r,
		func(ctx context.Context, e application.Execution) error {
			var err error
			artifact, err = (spec.SpecForge{}).Generate(ctx, c, e.Scope, b)
			runs++
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	// Queue admission is not a permanent grant: revoke before first dispatch.
	granted = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 0 {
		t.Fatal(err)
	}
	granted = true
	if err := w.Start(t.Context(), "org", "job"); err != nil || runs != 1 {
		t.Fatal(err)
	}
	hash := artifact.ContentHash()
	allowed = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 1 {
		t.Fatal(err)
	}
	allowed = true
	if err := w.Start(t.Context(), "org", "job"); err != nil || runs != 2 || artifact.ContentHash() != hash {
		t.Fatal(err)
	}
	if err := w.Start(t.Context(), "other", "job"); !errors.Is(err, application.ErrWorkloadNotFound) || runs != 2 {
		t.Fatal(err)
	}
}
