package billingstore

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"safepatch/internal/domain/billing"
	org "safepatch/internal/domain/organization"
	"testing"
	"time"
)

type readerFunc func(context.Context, billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error)

func (f readerFunc) ReadSubscription(c context.Context, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
	return f(c, r)
}
func observation() billing.SubscriptionSnapshot {
	return billing.SubscriptionSnapshot{OrganizationID: "org", Provider: "stripe", ExternalReference: "sub_provider", EditionVersionID: "team-2026-v2", State: org.Subscribed, CreatedAt: stamp().Add(-time.Hour), AccessUntil: stamp().Add(time.Hour), PaymentSettled: true}
}
func lockedSteps(applied any) []step {
	return []step{
		{query: "BEGIN", args: []driver.Value{int64(2), false}},
		{query: "FOR UPDATE OF o", args: []driver.Value{"conn", "evt", "stripe", "acct_test", false}, rows: [][]driver.Value{{"org", "sub_provider"}}},
		{query: "SELECT applied_at", args: []driver.Value{"conn", "evt"}, rows: [][]driver.Value{{applied}}},
	}
}
func TestDurableReceiptDeduplicatesAndRejectsIdentityReuse(t *testing.T) {
	n := billing.Notification{Provider: "stripe", EventID: "evt", OrganizationID: "org", ExternalReference: "sub_provider", OccurredAt: stamp()}
	for _, mode := range []string{"new", "duplicate", "collision", "database_error"} {
		t.Run(mode, func(t *testing.T) {
			insert := step{query: "ON CONFLICT(connection_id,event_id) DO NOTHING", args: []driver.Value{"conn", "evt", "org", "sub_provider", stamp(), "stripe", "acct_test", false}}
			rows := [][]driver.Value{{"org", "sub_provider", stamp()}}
			if mode == "collision" {
				rows[0][0] = "other"
			}
			steps := []step{insert, {query: "SELECT organization_id,external_reference,occurred_at", args: []driver.Value{"conn", "evt"}, rows: rows}}
			if mode == "database_error" {
				steps = steps[:1]
				steps[0].err = errors.New("backend unavailable")
			}
			s, _ := scripted(t, steps...)
			err := s.Accept(t.Context(), n)
			if (err == nil) != (mode == "new" || mode == "duplicate") {
				t.Fatal(err)
			}
		})
	}
}
func TestReconcileCommitsSubscriptionStateHistoryAndReceiptAtomically(t *testing.T) {
	for _, mode := range []string{"create", "update", "duplicate", "fetch_error", "ownership", "state_conflict", "history_error", "commit_error", "terminal_resurrection"} {
		t.Run(mode, func(t *testing.T) {
			v := observation()
			steps := lockedSteps(nil)
			if mode == "duplicate" {
				steps = lockedSteps(stamp())
				steps = append(steps, step{query: "COMMIT"})
			} else if mode == "fetch_error" || mode == "ownership" {
				steps = append(steps, step{query: "ROLLBACK"})
			} else {
				steps = append(steps, bundleSteps("team-2026-v2", "team")...)
				old := step{query: "FROM subscriptions WHERE provider=$1 AND external_reference=$2", args: []driver.Value{"stripe", "sub_provider"}}
				if mode == "update" || mode == "terminal_resurrection" {
					state := "ACTIVE"
					if mode == "terminal_resurrection" {
						state = "CANCELED"
					}
					old.rows = [][]driver.Value{{"org", "sub_internal", state, int64(1), v.CreatedAt, stamp()}}
				}
				steps = append(steps, old)
				if mode == "terminal_resurrection" {
					steps = append(steps, step{query: "ROLLBACK"})
				} else {
					write := step{query: "INSERT INTO subscriptions", affected: 1}
					if mode == "update" {
						write = step{query: "UPDATE subscriptions", args: []driver.Value{"org", "sub_internal", "team-2026-v2", "ACTIVE", int64(2), stamp(), int64(1)}, affected: 1}
					}
					steps = append(steps, write, step{query: "INSERT INTO billing_subscription_state", affected: 1})
					if mode == "state_conflict" {
						steps[len(steps)-1].affected = 0
						steps = append(steps, step{query: "ROLLBACK"})
					} else {
						steps = append(steps, step{query: "INSERT INTO billing_subscription_history", affected: 1, check: func(a []driver.NamedValue) {
							var snapshot struct {
								Subscription org.Subscription
								Observation  billing.SubscriptionSnapshot
							}
							if json.Unmarshal([]byte(a[5].Value.(string)), &snapshot) != nil || snapshot.Subscription.OrganizationID != "org" || snapshot.Observation != v {
								t.Error("corrupt audit history")
							}
						}})
						if mode == "history_error" {
							steps[len(steps)-1].err = errors.New("history failed")
							steps = append(steps, step{query: "ROLLBACK"})
						} else {
							steps = append(steps, step{query: "UPDATE billing_events SET applied_at", args: []driver.Value{"conn", "evt", stamp()}, affected: 1}, step{query: "COMMIT"})
							if mode == "commit_error" {
								steps[len(steps)-1].err = errors.New("commit failed")
							}
						}
					}
				}
			}
			s, c := scripted(t, steps...)
			calls := 0
			duplicate, err := s.Reconcile(t.Context(), "evt", readerFunc(func(_ context.Context, r billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
				calls++
				if !c.active || len(c.steps) == 0 || r.OrganizationID != "org" || r.ExternalReference != "sub_provider" {
					t.Error("fetch without transaction/tenant")
				}
				if mode == "fetch_error" {
					return billing.SubscriptionSnapshot{}, billing.ErrProvider
				}
				if mode == "ownership" {
					v.OrganizationID = "foreign"
				}
				return v, nil
			}))
			want := mode == "create" || mode == "update" || mode == "duplicate"
			if (err == nil) != want || duplicate != (mode == "duplicate") {
				t.Fatal(duplicate, err)
			}
			if (calls == 0) != (mode == "duplicate") {
				t.Fatal("duplicate fetched provider")
			}
		})
	}
}
func TestCustomerConnectionAndPendingBoundaries(t *testing.T) {
	s, _ := scripted(t,
		step{query: "SELECT b.customer_reference", args: []driver.Value{"conn", "org", "stripe", "acct_test", false}, rows: [][]driver.Value{{"cus_owned"}}},
		step{query: "SELECT b.organization_id", args: []driver.Value{"conn", "cus_owned", "stripe", "acct_test", false}, rows: [][]driver.Value{{"org"}}},
		step{query: "applied_at IS NULL", args: []driver.Value{"conn", "", int64(2)}, rows: [][]driver.Value{{"evt_a"}, {"evt_b"}}})
	if id, err := s.CustomerForOrganization(t.Context(), "stripe", "org"); err != nil || id != "cus_owned" {
		t.Fatal(id, err)
	}
	if id, err := s.OrganizationForCustomer(t.Context(), "stripe", "cus_owned"); err != nil || id != "org" {
		t.Fatal(id, err)
	}
	if ids, err := s.Pending(t.Context(), "", 2); err != nil || len(ids) != 2 {
		t.Fatal(ids, err)
	}
	if _, err := s.Pending(t.Context(), "", 10000); err == nil {
		t.Fatal("unbounded retry scan")
	}
	if _, err := s.CustomerForOrganization(t.Context(), "other", "org"); err == nil {
		t.Fatal("foreign provider")
	}
	if err := s.Accept(t.Context(), billing.Notification{}); err == nil {
		t.Fatal("unverified identity shape")
	}
}

func TestReconciliationDirectoryReadsReuseSingleConnectionTransaction(t *testing.T) {
	steps := append(lockedSteps(nil), step{query: "SELECT b.customer_reference", args: []driver.Value{"conn", "org", "stripe", "acct_test", false}, rows: [][]driver.Value{{"cus_owned"}}}, step{query: "ROLLBACK"})
	s, _ := scripted(t, steps...)
	s.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err := s.Reconcile(ctx, "evt", readerFunc(func(c context.Context, _ billing.SubscriptionRequest) (billing.SubscriptionSnapshot, error) {
		id, err := s.CustomerForOrganization(c, "stripe", "org")
		if err != nil || id != "cus_owned" {
			t.Fatal("directory blocked on held connection", id, err)
		}
		return billing.SubscriptionSnapshot{}, billing.ErrProvider
	}))
	if !errors.Is(err, billing.ErrProvider) {
		t.Fatal(err)
	}
}
