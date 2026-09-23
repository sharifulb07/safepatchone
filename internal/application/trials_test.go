package application_test

import (
	"context"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/trial"
	"safepatch/internal/tenant"
	"strings"
	"testing"
	"time"
)

type trialRepo struct {
	calls   int
	foreign bool
	err     error
}

func (r *trialRepo) Start(_ context.Context, org string, key edition.Key) (trial.Trial, error) {
	r.calls++
	if r.foreign {
		org = "foreign"
	}
	p, _ := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "", 0)
	o, _ := p.Offer(key)
	v, _ := trial.New(org, o, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	return v, r.err
}
func (r *trialRepo) Get(ctx context.Context, org string) (trial.Trial, error) {
	return r.Start(ctx, org, edition.Team)
}
func TestTrialManagementTenantPermissionAndErrorBoundaries(t *testing.T) {
	for _, op := range []string{"start", "get"} {
		for _, mode := range []string{"owner", "viewer", "inactive", "membership_error", "foreign_scope", "foreign_record", "store_error", "canceled", "invalid_actor"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				r := &trialRepo{foreign: mode == "foreign_record"}
				if mode == "store_error" {
					r.err = errors.New("private database")
				}
				m := billingMemberships{active: mode != "inactive"}
				if mode == "membership_error" {
					m.err = errors.New("private membership")
				}
				permissions, _ := authorization.NewPermissionService(m)
				tenants := tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
					if mode == "foreign_scope" {
						s.OrganizationID = "foreign"
					}
					return s, nil
				})
				s, _ := application.NewTrialService(r, tenants, permissions)
				org := "org_a"
				if mode == "viewer" {
					org = "org_b"
				}
				actor := authorization.Principal{ID: "user", Kind: authorization.User}
				if mode == "invalid_actor" {
					actor.Kind = "LLM"
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "canceled" {
					cancel()
				}
				var v trial.Trial
				var err error
				if op == "start" {
					v, err = s.Start(ctx, actor, org, edition.Team)
				} else {
					v, err = s.Get(ctx, actor, org)
				}
				if (err == nil) != (mode == "owner") {
					t.Fatal(v, err)
				}
				if err != nil && (v != (trial.Trial{}) || strings.Contains(err.Error(), "private")) {
					t.Fatal("partial/private result", v, err)
				}
				want := 0
				if mode == "owner" || mode == "foreign_record" || mode == "store_error" {
					want = 1
				}
				if r.calls != want {
					t.Fatal("denied boundary reached store", r.calls)
				}
			})
		}
	}
}
