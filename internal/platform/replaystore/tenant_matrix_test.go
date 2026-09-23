package replaystore_test

import (
	"context"
	"safepatch/internal/application"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/replay"
	"testing"
	"time"
)

type tenantFirstReplayGrants struct {
	*grants
	t *testing.T
}

func (g tenantFirstReplayGrants) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	if org != "org" {
		g.t.Fatal("foreign tenant reached entitlement resolution")
	}
	return g.grants.Has(ctx, org, key)
}

func TestReplayForeignDatasetClaimsAndDispatchAcrossEditions(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				f := setup(t)
				r, _ := replay.NewRedactor([]string{"/value"})
				engine, _ := replay.NewEngine(f.sandbox, r, observer{})
				var err error
				f.service, err = application.NewReplayService(tenants{}, tenantFirstReplayGrants{f.grants, t}, f.grants, f.grants, f.quota, f.store, r, engine, func() time.Time { return f.now })
				if err != nil {
					t.Fatal(err)
				}
				before := f.dataset.Digest()
				foreign := replay.Scope{OrganizationID: "foreign_" + string(a.Key) + "_" + string(b.Key), RepositoryID: f.scope.RepositoryID}
				if _, err := f.store.Get(t.Context(), foreign, "data"); err == nil {
					t.Fatal("foreign dataset read")
				}
				if err := f.store.Claim(t.Context(), foreign, "same_run", before); err == nil {
					t.Fatal("foreign dataset used in claim")
				}
				if _, err := f.service.Run(t.Context(), f.actor, foreign, "data", "same_run"); err == nil {
					t.Fatal("foreign replay dispatched")
				}
				stored, err := f.store.Get(t.Context(), f.scope, "data")
				if err != nil || stored.Digest() != before || f.sandbox.calls.Load() != 0 {
					t.Fatal("adversarial request changed victim")
				}
			})
		}
	}
}
