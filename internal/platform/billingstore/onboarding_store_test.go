package billingstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/platform/onboardingstore"
	"testing"
)

func TestOnboardingReadsOnlyAuthoritativeSubscriptionAndTrialState(t *testing.T) {
	for _, mode := range []string{"pending", "active", "database_error", "community"} {
		t.Run(mode, func(t *testing.T) {
			steps := []step{{query: "e.sealed", args: []driver.Value{"team-2026-v2", "org"}, rows: [][]driver.Value{{"team", true}}}}
			if mode == "community" {
				steps[0].rows[0][0] = "community"
			} else {
				next := step{query: "b.revision=s.revision", args: []driver.Value{"org", "team-2026-v2", stamp()}}
				if mode == "active" {
					next.rows = [][]driver.Value{{"SUBSCRIPTION", stamp().AddDate(0, 1, 0)}}
				}
				if mode == "database_error" {
					next.err = errors.New("database failed")
				}
				steps = append(steps, next)
			}
			s, _ := scripted(t, steps...)
			store, _ := onboardingstore.New(s.db, stamp)
			v, err := store.Access(t.Context(), "org", "team-2026-v2")
			if v.Ready != (mode == "active" || mode == "community") || (err != nil) != (mode == "database_error") {
				t.Fatal(v, err)
			}
		})
	}
}
func TestOnboardingStateCASDoesNotWriteCommercialSources(t *testing.T) {
	for _, mode := range []string{"saved", "stale", "write_error"} {
		t.Run(mode, func(t *testing.T) {
			v := flow.State{OrganizationID: "org", Revision: 1, Edition: "team", EditionVersionID: "team-2026-v2", Repositories: []flow.Repository{}}
			steps := []step{{query: "BEGIN"}, {query: "FOR UPDATE", args: []driver.Value{"org"}, rows: [][]driver.Value{{int64(1)}}}}
			if mode == "stale" {
				steps[1].rows[0][0] = int64(2)
				steps = append(steps, step{query: "ROLLBACK"})
			} else {
				write := step{query: "UPDATE onboarding_states", check: func(args []driver.NamedValue) {
					var stored flow.State
					if json.Unmarshal([]byte(args[3].Value.(string)), &stored) != nil || stored.Revision != 2 || stored.Edition != "team" {
						t.Fatal("invalid stored intent")
					}
				}}
				end := "COMMIT"
				if mode == "write_error" {
					write.err = errors.New("failed")
					end = "ROLLBACK"
				}
				steps = append(steps, write, step{query: end})
			}
			s, _ := scripted(t, steps...)
			store, _ := onboardingstore.New(s.db, stamp)
			got, err := store.Save(t.Context(), v, 1)
			if (err == nil) != (mode == "saved") {
				t.Fatal(got, err)
			}
		})
	}
}

type offlinePublisher struct{ calls int }

func (p *offlinePublisher) Publish(context.Context, string, string) error {
	p.calls++
	return errors.New("broker offline")
}
func TestFirstProofSubmissionRemainsPendingWithoutBrokerAcknowledgement(t *testing.T) {
	v := flow.State{OrganizationID: "org", Revision: 3, Edition: "team", EditionVersionID: "team-2026-v2", InstallationID: 1, Repositories: []flow.Repository{{ID: "gh_1", FullName: "owner/repo"}}, PolicyID: "baseline-v1"}
	raw, _ := json.Marshal(v)
	steps := []step{{query: "BEGIN"}, {query: "FOR UPDATE", rows: [][]driver.Value{{string(raw)}}}, {query: "INSERT INTO onboarding_proof_jobs", check: func(args []driver.NamedValue) {
		if args[0].Value != "org" || args[2].Value != "user" || args[4].Value != string(raw) {
			t.Fatal("queue identity drift")
		}
	}}, {query: "UPDATE onboarding_states"}, {query: "COMMIT"}, {query: "SELECT status FROM onboarding_proof_jobs", rows: [][]driver.Value{{"PENDING"}}}}
	s, _ := scripted(t, steps...)
	store, _ := onboardingstore.New(s.db, stamp)
	publisher := &offlinePublisher{}
	proofs, _ := onboardingstore.NewProofs(store, publisher)
	got, err := proofs.Submit(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, v, "attempt")
	if err != nil || got.Status != "PENDING" || publisher.calls != 1 {
		t.Fatal(got, err)
	}
	var _ application.OnboardingProofs = proofs
}
