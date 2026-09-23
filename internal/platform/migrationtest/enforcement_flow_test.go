package migrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/httpapi"
	"safepatch/internal/tenant"
)

type enforcementJobs struct{ job application.Workload }

func (s *enforcementJobs) Get(ctx context.Context, org, id string) (application.Workload, error) {
	if err := ctx.Err(); err != nil {
		return application.Workload{}, err
	}
	if org != s.job.Target.OrganizationID || id != s.job.ID {
		return application.Workload{}, application.ErrWorkloadNotFound
	}
	return s.job, nil
}

type enforcementMembers struct{ revoked bool }

func (m *enforcementMembers) GetMembership(ctx context.Context, org string, actor authorization.Principal) (authorization.Membership, error) {
	r, err := (&fixtureMemberships{}).GetMembership(ctx, org, actor)
	if m.revoked {
		r.Active = false
	}
	return r, err
}

// Integrates real migration bundles, revision-checked snapshots, independent
// permissions, HTTP middleware, CLI preflight and worker dispatch. Only identity,
// storage and the operation itself are fixtures; no premium engine is simulated.
func TestAPIAndWorkerRemainAuthoritativeAcrossEditionChanges(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range initialBundles(t) {
		t.Run(string(bundle.Version.EditionKey), func(t *testing.T) {
			source := &seededSnapshotSource{state: domain.SnapshotState{OrganizationID: "org_customer", Edition: bundle.Version.EditionKey, EditionVersion: bundle.Version.EditionVersionID, SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}}
			ownsReplay := false
			for _, mapping := range bundle.Features {
				record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "ent_" + mapping.FeatureID, OrganizationID: "org_customer", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled, LimitValue: mapping.LimitValue, StartsAt: bundle.Version.CreatedAt, CreatedAt: bundle.Version.CreatedAt}, fc)
				if err != nil {
					t.Fatal(err)
				}
				source.records = append(source.records, record)
				definition, _ := fc.GetByID(mapping.FeatureID)
				if definition.Key == feature.KeyRuntimeReplay {
					ownsReplay = mapping.Enabled
				}
			}
			service, err := entitlement.NewSnapshotService(source, fc, func() time.Time { return now }, time.Minute, 2)
			if err != nil {
				t.Fatal(err)
			}
			members := &enforcementMembers{}
			permissions, _ := authorization.NewPermissionService(members)
			quota := &fixtureQuota{}
			target := tenant.Scope{OrganizationID: "org_customer", ResourceID: "proof_1"}
			store := &enforcementJobs{job: application.Workload{ID: "job_1", Kind: "replay", Actor: authorization.Principal{ID: "owner", Kind: authorization.User}, Target: target}}
			jobBefore, _ := json.Marshal(store.job)
			history := source.records
			historyBefore, _ := json.Marshal(history)
			starts := 0
			worker, err := application.NewWorkerOperation(store, fixtureIdentity{}, service, permissions, quota, "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(context.Context, application.Execution) error { starts++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			middleware, err := httpapi.NewEntitlementMiddleware(httpapi.EnforcementConfig{Authenticator: fixtureIdentity{}, Tenants: fixtureIdentity{}, Entitlements: service, Permissions: permissions, Quotas: quota, Permission: authorization.KeyReplayExecute, Request: func(r *http.Request) (application.Request, error) {
				return application.Request{Credential: r.Header.Get("Authorization"), Target: target}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			wrap, err := middleware.RequireEntitlement("runtime.replay")
			if err != nil {
				t.Fatal(err)
			}
			admissions := 0
			handler := wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { admissions++; w.WriteHeader(204) }))
			request := func(actor string) int {
				r := httptest.NewRequest("POST", "/replay?entitled=true", nil)
				r.Header.Set("Authorization", actor)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w.Code
			}
			if status := request("viewer"); status != 403 || admissions != 0 {
				t.Fatal("viewer bypass", status)
			}
			if err := application.RequireFeature(t.Context(), service, "org_customer", feature.KeyRuntimeReplay); (err == nil) != ownsReplay {
				t.Fatal("CLI boundary", err)
			}
			status := request("owner")
			if !ownsReplay {
				if status != 403 || admissions != 0 {
					t.Fatal("direct API bypass", status)
				}
				if err := worker.Start(t.Context(), "org_customer", "job_1"); !errors.Is(err, entitlement.ErrNotEntitled) || starts != 0 {
					t.Fatal("direct worker bypass", err)
				}
				return
			}
			if status != 204 || admissions != 1 {
				t.Fatal("entitled owner blocked", status)
			}
			if err := worker.Start(t.Context(), "org_customer", "job_1"); err != nil || starts != 1 {
				t.Fatal(err)
			}
			// Enqueue/preflight has succeeded. Changed permissions still stop work.
			members.revoked = true
			if err := worker.Start(t.Context(), "org_customer", "job_1"); !errors.Is(err, application.ErrPermissionDenied) || starts != 1 {
				t.Fatal("worker trusted old permission", err)
			}
			members.revoked = false
			// Without explicit cache invalidation, the changed source revision must
			// invalidate both API and worker ownership before another operation.
			source.records = nil
			source.state.SourceVersions.Subscription++
			if err := worker.Start(t.Context(), "org_customer", "job_1"); !errors.Is(err, entitlement.ErrNotEntitled) || starts != 1 {
				t.Fatal("worker trusted enqueue entitlement", err)
			}
			if status := request("owner"); status != 403 || admissions != 1 {
				t.Fatal("server reused earlier admission", status)
			}
			// Restoring policy can admit again without deleting queued or historical data.
			source.records = history
			source.state.SourceVersions.Subscription++
			if err := worker.Start(t.Context(), "org_customer", "job_1"); err != nil || starts != 2 {
				t.Fatal(err)
			}
			jobAfter, _ := json.Marshal(store.job)
			historyAfter, _ := json.Marshal(history)
			if string(jobBefore) != string(jobAfter) || string(historyBefore) != string(historyAfter) {
				t.Fatal("policy changes corrupted data")
			}
			if err := worker.Start(t.Context(), "org_other", "job_1"); !errors.Is(err, application.ErrWorkloadNotFound) {
				t.Fatal("cross-tenant job access", err)
			}
		})
	}
}

func TestWorkerRejectsTrialExpiredAfterCLIAdmission(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Second)
	fc, err := feature.NewBuiltinCatalog("1.0.0", now)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := fc.Get(feature.KeyRuntimeReplay)
	if err != nil {
		t.Fatal(err)
	}
	record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "trial_replay", OrganizationID: "org_customer", FeatureID: replay.FeatureID, Source: domain.SourceTrial, SourceReference: "trial_1", Enabled: true, StartsAt: now.Add(-time.Hour), ExpiresAt: &expires, CreatedAt: now}, fc)
	if err != nil {
		t.Fatal(err)
	}
	source := &seededSnapshotSource{state: domain.SnapshotState{OrganizationID: "org_customer", Edition: "community", EditionVersion: "community-2026-v2", SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}, records: []domain.Entitlement{record}}
	service, err := entitlement.NewSnapshotService(source, fc, func() time.Time { return now }, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := application.RequireFeature(t.Context(), service, "org_customer", feature.KeyRuntimeReplay); err != nil {
		t.Fatal(err)
	}
	permissions, err := authorization.NewPermissionService(&fixtureMemberships{})
	if err != nil {
		t.Fatal(err)
	}
	jobs := &enforcementJobs{job: application.Workload{ID: "job_1", Kind: "replay", Actor: authorization.Principal{ID: "owner", Kind: authorization.ServiceAccount}, Target: tenant.Scope{OrganizationID: "org_customer", ResourceID: "proof_1"}}}
	quota := &fixtureQuota{}
	worker, err := application.NewWorkerOperation(jobs, fixtureIdentity{}, service, permissions, quota, "replay", application.Requirement{Feature: feature.KeyRuntimeReplay, Permission: authorization.KeyReplayExecute}, func(context.Context, application.Execution) error { t.Fatal("expired trial executed"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	now = expires
	if err := worker.Start(t.Context(), "org_customer", "job_1"); !errors.Is(err, entitlement.ErrNotEntitled) || quota.calls != 0 {
		t.Fatal("expired cached admission reused", err)
	}
}
