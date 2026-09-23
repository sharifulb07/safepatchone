package scanners

import (
	"context"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
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
	return p.enabled && (org == "org_a" || org == entitlement.LocalOrganizationID) && key == authorization.KeyProofExecute, nil
}

type memberships struct{}

func (memberships) Resolve(_ context.Context, actor authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
	if actor.ID != "actor" || scope.ResourceID != "repo" || (scope.OrganizationID != "org_a" && scope.OrganizationID != entitlement.LocalOrganizationID) {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return scope, nil
}

type quota struct {
	deny  bool
	calls int
}

var errQuota = errors.New("quota exhausted")

func (q *quota) Check(context.Context, application.Execution) error {
	q.calls++
	if q.deny {
		return errQuota
	}
	return nil
}

type workStore struct{ job application.Workload }

func (w workStore) Get(_ context.Context, org, id string) (application.Workload, error) {
	if org != w.job.Target.OrganizationID || id != w.job.ID {
		return application.Workload{}, application.ErrWorkloadNotFound
	}
	return w.job, nil
}

func TestHostedScannerBoundaries(t *testing.T) {
	catalog, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			a, input := fixture(t, tool)
			requirement, err := application.ScannerVerificationRequirement(catalog, input.Capability, application.ScannerHosted)
			if err != nil || requirement.Feature != feature.KeyVerifySecurity || requirement.Permission != authorization.KeyProofExecute {
				t.Fatal(requirement, err)
			}
			g, p, q := &grants{enabled: true}, &permissions{enabled: true}, &quota{}
			job := application.Workload{ID: "job", Kind: "scanner", Actor: authorization.Principal{ID: "actor", Kind: authorization.User}, Target: tenant.Scope{OrganizationID: "org_a", ResourceID: "repo"}}
			store := workStore{job}
			archive := &memoryStore{}
			runs := 0
			var records []Result
			executor, _ := NewExecutor(a, resultRunner(func(ctx context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
				runs++
				return output(clean[tool], 0)(ctx, r)
			}), archive, time.Second)
			worker, err := application.NewWorkerOperation(store, memberships{}, g, p, q, "scanner", requirement, func(ctx context.Context, e application.Execution) error {
				r, err := executor.Run(ctx, input, evidence.ArchiveScope{OrganizationID: e.Scope.OrganizationID, Subject: input.Target.Subject})
				if err == nil {
					records = append(records, r)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := worker.Start(t.Context(), "org_a", "job"); err != nil || runs != 1 {
				t.Fatal(runs, err)
			}
			g.enabled = false
			if err := worker.Start(t.Context(), "org_a", "job"); !errors.Is(err, entitlement.ErrNotEntitled) || runs != 1 || q.calls != 1 {
				t.Fatal("entitlement bypass", err)
			}
			g.enabled = true
			p.enabled = false
			if err := worker.Start(t.Context(), "org_a", "job"); !errors.Is(err, application.ErrPermissionDenied) || runs != 1 || q.calls != 1 {
				t.Fatal("permission bypass", err)
			}
			p.enabled = true
			q.deny = true
			if err := worker.Start(t.Context(), "org_a", "job"); !errors.Is(err, errQuota) || runs != 1 {
				t.Fatal("quota bypass", err)
			}
			q.deny = false
			if err := worker.Start(t.Context(), "org_b", "job"); !errors.Is(err, application.ErrWorkloadNotFound) || runs != 1 {
				t.Fatal("tenant bypass", err)
			}
			if err := worker.Start(t.Context(), "org_a", "job"); err != nil || runs != 2 {
				t.Fatal(runs, err)
			}
			if store.job != job || len(records) != 2 || *records[0].Manifest != *records[1].Manifest {
				t.Fatal("grant change modified immutable evidence")
			}
		})
	}
}

func TestCommunityInstalledScannerAccess(t *testing.T) {
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	community, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		a, input := fixture(t, tool)
		requirement, err := application.ScannerVerificationRequirement(catalog, input.Capability, application.ScannerLocal)
		if err != nil {
			t.Fatal(err)
		}
		p, q := &permissions{enabled: true}, &quota{}
		guard, err := application.NewExecutionGuard(memberships{}, community, p, q, requirement)
		if err != nil {
			t.Fatal(err)
		}
		actor := authorization.Principal{ID: "actor", Kind: authorization.User}
		scope := tenant.Scope{OrganizationID: entitlement.LocalOrganizationID, ResourceID: "repo"}
		if _, err := guard.Authorize(t.Context(), actor, scope); err != nil {
			t.Fatal("offline local denied", tool, err)
		}
		executor, _ := NewExecutor(a, output(clean[tool], 0), &memoryStore{}, time.Second)
		if r, err := executor.Run(t.Context(), input, evidence.ArchiveScope{OrganizationID: scope.OrganizationID, Subject: input.Target.Subject}); err != nil || r.Record.Report.Status != evidence.StatusPass {
			t.Fatal(r, err)
		}
		p.enabled = false
		if _, err := guard.Authorize(t.Context(), actor, scope); !errors.Is(err, application.ErrPermissionDenied) {
			t.Fatal("local permission bypass", err)
		}
		hosted, _ := application.ScannerVerificationRequirement(catalog, input.Capability, application.ScannerHosted)
		allowed, err := community.Has(t.Context(), "org_hosted", hosted.Feature)
		if !errors.Is(err, entitlement.ErrOrganizationMismatch) || allowed {
			t.Fatal("community simulated hosted access")
		}
	}
	for _, location := range []application.ScannerLocation{"", "enterprise"} {
		if _, err := application.ScannerVerificationRequirement(catalog, "scanner.syft", location); err == nil {
			t.Fatal("unknown location")
		}
	}
	if _, err := application.ScannerVerificationRequirement(catalog, "scanner.unknown", application.ScannerLocal); err == nil {
		t.Fatal("unknown scanner")
	}
	if _, err := application.ScannerVerificationRequirement(nil, "scanner.syft", application.ScannerLocal); err == nil {
		t.Fatal("nil catalog")
	}
}
