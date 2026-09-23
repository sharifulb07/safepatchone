package golang

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
)

type grants struct {
	entitlement.EntitlementService
	enabled bool
}

func (g *grants) Has(_ context.Context, org string, key feature.Key) (bool, error) {
	return g.enabled && org == "org_a" && key == feature.KeyVerifySecurity, nil
}

type permissions struct{ enabled bool }

func (p *permissions) Allowed(_ context.Context, _ authorization.Principal, org string, key authorization.Key) (bool, error) {
	return p.enabled && org == "org_a" && key == authorization.KeyProofExecute, nil
}

type memberships struct{}

func (memberships) Resolve(_ context.Context, actor authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
	if actor.ID != "actor" || scope.OrganizationID != "org_a" || scope.ResourceID != "repo" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return scope, nil
}

type unmetered struct{}

func (unmetered) Check(context.Context, application.Execution) error { return nil }

type workStore struct{ job application.Workload }

func (w workStore) Get(_ context.Context, org, id string) (application.Workload, error) {
	if org != w.job.Target.OrganizationID || id != w.job.ID {
		return application.Workload{}, application.ErrWorkloadNotFound
	}
	return w.job, nil
}

type resultRunner func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error)

func (f resultRunner) Run(c context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
	return f(c, r)
}

func TestCatalogMappingAndPremiumBoundaries(t *testing.T) {
	catalog, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	v, input := fixture(t)
	for _, cap := range v.Capabilities() {
		requirement, err := application.GoVerificationRequirement(catalog, cap)
		if err != nil {
			t.Fatal(err)
		}
		want := feature.KeyVerifyBasic
		if cap == Staticcheck || cap == Vulncheck {
			want = feature.KeyVerifySecurity
		}
		if requirement.Feature != want || requirement.Permission != authorization.KeyProofExecute {
			t.Fatal(cap, requirement)
		}
	}
	if _, err := application.GoVerificationRequirement(catalog, "go.unknown"); err == nil {
		t.Fatal("unknown feature admitted")
	}
	for _, cap := range []verification.Capability{Staticcheck, Vulncheck} {
		t.Run(string(cap), func(t *testing.T) {
			input.Capability = cap
			registry, _ := verification.NewRegistry(v)
			requirement, _ := application.GoVerificationRequirement(catalog, cap)
			g, p := &grants{enabled: true}, &permissions{enabled: true}
			runs := 0
			var records [][]byte
			job := application.Workload{ID: "job", Kind: "go_security", Actor: authorization.Principal{ID: "actor", Kind: authorization.User}, Target: tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"}}
			store := workStore{job}
			worker, err := application.NewWorkerOperation(store, memberships{}, g, p, unmetered{}, "go_security", requirement, func(ctx context.Context, _ application.Execution) error {
				record, err := registry.Run(ctx, ID, input, resultRunner(func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
					runs++
					stdout := ""
					if cap == Vulncheck {
						stdout = `{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scan_level":"symbol","db":"file:///trusted/vulndb"}}` + "\n" + `{"SBOM":{"roots":["example.test/root"]}}`
					}
					return verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Sandboxed: true, Stdout: []byte(stdout)}, nil
				}))
				if err != nil {
					return err
				}
				data, err := record.CanonicalJSON()
				if err != nil {
					return err
				}
				records = append(records, data)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := worker.Start(t.Context(), "org_a", "job"); err != nil || runs != 1 {
				t.Fatal(runs, err)
			}
			g.enabled = false
			if err := worker.Start(t.Context(), "org_a", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 {
				t.Fatal("unentitled access", runs, err)
			}
			g.enabled = true
			p.enabled = false
			if err := worker.Start(t.Context(), "org_a", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 1 {
				t.Fatal("permission bypass", runs, err)
			}
			p.enabled = true
			if err := worker.Start(t.Context(), "org_b", "job"); !errors.Is(err, application.ErrWorkloadNotFound) || runs != 1 {
				t.Fatal("tenant bypass", runs, err)
			}
			if err := worker.Start(t.Context(), "org_a", "job"); err != nil || runs != 2 {
				t.Fatal(runs, err)
			}
			if store.job != job || len(records) != 2 || !bytes.Equal(records[0], records[1]) {
				t.Fatal("grant change corrupted work or evidence")
			}
		})
	}
}
