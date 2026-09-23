package languages_test

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
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/sir"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
)

type membershipFunc func(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error)

func (f membershipFunc) Resolve(c context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return f(c, p, s)
}

type grantFunc struct {
	entitlement.EntitlementService
	has func(context.Context, string, feature.Key) (bool, error)
}

func (f grantFunc) Has(c context.Context, org string, k feature.Key) (bool, error) {
	return f.has(c, org, k)
}

type permissionFunc func(context.Context, authorization.Principal, string, authorization.Key) (bool, error)

func (f permissionFunc) Allowed(c context.Context, p authorization.Principal, org string, k authorization.Key) (bool, error) {
	return f(c, p, org, k)
}

type quotaFunc func(context.Context, application.Execution) error

func (f quotaFunc) Check(c context.Context, e application.Execution) error { return f(c, e) }

type jobFunc func(context.Context, string, string) (application.Workload, error)

func (f jobFunc) Get(c context.Context, org, id string) (application.Workload, error) {
	return f(c, org, id)
}

func TestEveryLanguagePackPremiumAdmissionAndPlanChangePreservation(t *testing.T) {
	r := registry(t)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	for _, d := range r.Discover() {
		var commonHash string
		for _, ed := range edition.Editions() {
			t.Run(d.ID+"/"+ed.Name, func(t *testing.T) {
				granted, allowed := true, true
				var calls []string
				service, err := application.NewLanguagePackService(r, fc,
					membershipFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
						calls = append(calls, "tenant")
						if p != actor || s != scope {
							return tenant.Scope{}, tenant.ErrInvalidScope
						}
						return s, nil
					}),
					grantFunc{has: func(_ context.Context, org string, key feature.Key) (bool, error) {
						calls = append(calls, "entitlement")
						if org != "org" || key != feature.KeyLanguageAdditionalPacks {
							t.Fatal("wrong feature", key)
						}
						return granted, nil
					}},
					permissionFunc(func(_ context.Context, p authorization.Principal, org string, key authorization.Key) (bool, error) {
						calls = append(calls, "permission")
						if p != actor || org != "org" || key != authorization.KeyProofExecute {
							t.Fatal("wrong permission")
						}
						return allowed, nil
					}),
					quotaFunc(func(_ context.Context, e application.Execution) error {
						calls = append(calls, "quota")
						if e.Scope != scope || e.Requirement.Feature != feature.KeyLanguageAdditionalPacks {
							t.Fatal("wrong quota")
						}
						return nil
					}))
				if err != nil {
					t.Fatal(err)
				}
				in := input(d.ID, "source.txt")
				first, err := service.Normalize(t.Context(), actor, scope, d.ID, in)
				if err != nil || !slices.Equal(calls, []string{"tenant", "entitlement", "permission", "quota"}) {
					t.Fatal(err, calls)
				}
				if commonHash == "" {
					commonHash = first.ContentHash()
				} else if commonHash != first.ContentHash() {
					t.Fatal("edition changed shared SIR")
				}
				original, _ := first.MarshalJSON()
				granted = false
				calls = nil
				denied, err := service.Normalize(t.Context(), actor, scope, d.ID, in)
				if !errors.Is(err, entitlement.ErrNotEntitled) || denied.ContentHash() != "" || !slices.Equal(calls, []string{"tenant", "entitlement"}) {
					t.Fatal(err, calls)
				}
				granted, allowed = true, false
				calls = nil
				denied, err = service.Normalize(t.Context(), actor, scope, d.ID, in)
				if !errors.Is(err, application.ErrPermissionDenied) || denied.ContentHash() != "" || slices.Contains(calls, "quota") {
					t.Fatal(err, calls)
				}
				allowed = true
				restored, err := service.Normalize(t.Context(), actor, scope, d.ID, in)
				after, _ := first.MarshalJSON()
				if err != nil || restored.ContentHash() != first.ContentHash() || !bytes.Equal(original, after) {
					t.Fatal("plan change corrupted data", err)
				}
			})
		}
	}
}

func TestCommunityLanguageDetectionIsOfflineAndNormalizationDenied(t *testing.T) {
	r := registry(t)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	community, _ := entitlement.NewCommunityEntitlementProvider()
	actor := authorization.Principal{ID: "local-user", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: entitlement.LocalOrganizationID, ResourceID: "repo"}
	allowed := true
	s, err := application.NewLanguagePackService(r, fc, membershipFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), community, permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return allowed, nil
	}), quotaFunc(func(context.Context, application.Execution) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, file string }{{"go", "main.go"}, {"javascript", "main.js"}, {"typescript", "main.ts"}, {"python", "main.py"}, {"java", "Main.java"}, {"rust", "main.rs"}, {"csharp", "Main.cs"}, {"kotlin", "Main.kt"}, {"ruby", "main.rb"}, {"php", "main.php"}} {
		in := input(tc.id, tc.file)
		in.TenantID = scope.OrganizationID
		result, err := s.Detect(t.Context(), actor, scope, tc.id, verification.Target{Subject: in.Subject, Files: []string{tc.file}})
		if err != nil || result.State != verification.Detected {
			t.Fatal(result, err)
		}
		if _, err := s.Normalize(t.Context(), actor, scope, tc.id, in); !errors.Is(err, entitlement.ErrNotEntitled) {
			t.Fatal(err)
		}
	}
	allowed = false
	if _, err := s.Detect(t.Context(), actor, scope, "go", verification.Target{Subject: input("go", "main.go").Subject, Files: []string{"main.go"}}); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal(err)
	}
}

func TestLanguageAdmissionFailsClosedAndChecksScope(t *testing.T) {
	r := registry(t)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	actor := authorization.Principal{ID: "actor", Kind: authorization.User}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	secret := errors.New("secret database credential")
	for _, scenario := range []string{"tenant", "mismatch", "grant", "permission", "quota", "foreign_tenant", "foreign_repository", "invalid_sir", "cancel", "nil_context"} {
		t.Run(scenario, func(t *testing.T) {
			s, err := application.NewLanguagePackService(r, fc,
				membershipFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if scenario == "tenant" {
						return s, secret
					}
					if scenario == "mismatch" {
						s.OrganizationID = "other"
					}
					return s, nil
				}),
				grantFunc{has: func(context.Context, string, feature.Key) (bool, error) {
					if scenario == "grant" {
						return true, secret
					}
					return true, nil
				}},
				permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
					if scenario == "permission" {
						return true, secret
					}
					return true, nil
				}),
				quotaFunc(func(context.Context, application.Execution) error {
					if scenario == "quota" {
						return secret
					}
					return nil
				}))
			if err != nil {
				t.Fatal(err)
			}
			in := input("python", "main.py")
			if scenario == "foreign_tenant" {
				in.TenantID = "other"
			}
			if scenario == "foreign_repository" {
				in.Subject.RepositoryID = "other"
			}
			if scenario == "invalid_sir" {
				in.Coverage = "PASS"
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
			out, err := s.Normalize(ctx, actor, scope, "python", in)
			if err == nil || out.ContentHash() != "" || strings.Contains(err.Error(), secret.Error()) {
				t.Fatal(out, err)
			}
		})
	}
	if _, err := application.LanguagePackRequirement(fc, languagepack.Verify); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal("generic language access admitted verifier execution")
	}
	if _, err := application.NewLanguagePackService(nil, fc, nil, nil, nil, nil); err == nil {
		t.Fatal("nil registry")
	}
	if _, err := application.NewLanguagePackService(r, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("nil catalog")
	}
	if _, err := application.NewLanguagePackService(r, fc, nil, nil, nil, nil); err == nil {
		t.Fatal("nil guards")
	}
	for _, s := range []*application.LanguagePackService{nil, {}} {
		if _, err := s.Normalize(t.Context(), actor, scope, "python", input("python", "main.py")); !errors.Is(err, application.ErrInvalidGuard) {
			t.Fatal(err)
		}
	}
}

func TestLanguageWorkerRechecksBeforeNormalizationAndStoresScopedSIR(t *testing.T) {
	r := registry(t)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	requirement, _ := application.LanguagePackRequirement(fc, languagepack.NormalizeSIR)
	actor := authorization.Principal{ID: "worker", Kind: authorization.ServiceAccount}
	scope := tenant.Scope{OrganizationID: "org", ResourceID: "repo"}
	granted, allowed := true, true
	runs := 0
	in := input("python", "main.py")
	var snapshot sir.Representation
	w, err := application.NewWorkerOperation(jobFunc(func(_ context.Context, org, id string) (application.Workload, error) {
		if org != "org" || id != "job" {
			return application.Workload{}, application.ErrWorkloadNotFound
		}
		return application.Workload{ID: id, Kind: "language-normalize", Actor: actor, Target: scope}, nil
	}), membershipFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), grantFunc{has: func(_ context.Context, _ string, key feature.Key) (bool, error) {
		if key != feature.KeyLanguageAdditionalPacks {
			t.Fatal(key)
		}
		return granted, nil
	}}, permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return allowed, nil
	}), quotaFunc(func(context.Context, application.Execution) error { return nil }), "language-normalize", requirement, func(ctx context.Context, e application.Execution) error {
		runs++
		var err error
		snapshot, err = r.Normalize(ctx, "python", in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	granted = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 0 {
		t.Fatal(err)
	}
	granted = true
	allowed = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 0 {
		t.Fatal(err)
	}
	allowed = true
	if err := w.Start(t.Context(), "org", "job"); err != nil || runs != 1 {
		t.Fatal(err)
	}
	archive := evidence.ArchiveScope{OrganizationID: "org", Subject: in.Subject}
	store, err := evidencestore.NewLocal(t.TempDir(), archive)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw, _ := snapshot.MarshalJSON()
	ref, err := store.Put(t.Context(), archive, raw)
	if err != nil {
		t.Fatal(err)
	}
	granted = false
	if err := w.Start(t.Context(), "org", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 {
		t.Fatal(err)
	}
	stored, err := store.Get(t.Context(), archive, ref)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal("downgrade damaged stored SIR", err)
	}
	restored, err := sir.Decode(t.Context(), stored)
	if err != nil || restored.ContentHash() != snapshot.ContentHash() {
		t.Fatal(err)
	}
	foreign := archive
	foreign.OrganizationID = "other"
	if _, err := store.Get(t.Context(), foreign, ref); err == nil {
		t.Fatal("cross-tenant read")
	}
}
