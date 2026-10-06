package billingstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"safepatch/internal/billing/paddle"
	"safepatch/internal/domain/billing"
	"testing"
)

func TestPaddleCheckoutCommitsReservationBeforeProviderWrite(t *testing.T) {
	id := billing.StableID("conn", "org", "attempt")
	session := billing.Session{ExternalReference: "txn_01h04vsc0qhwtsbsxh3422wjs4", URL: "https://app.example/checkout?_ptxn=txn_01h04vsc0qhwtsbsxh3422wjs4"}
	s, c := scripted(t, step{query: "BEGIN"}, step{query: "FOR UPDATE", rows: [][]driver.Value{{true}}}, step{query: "SELECT offer_id"}, step{query: "SELECT EXISTS", rows: [][]driver.Value{{false}}}, step{query: "INSERT INTO paddle_checkouts", args: []driver.Value{"conn", id, "org", "team", "acct_test", false}, affected: 1}, step{query: "COMMIT"}, step{query: "UPDATE paddle_checkouts", args: []driver.Value{"conn", id, session.ExternalReference, session.URL}, affected: 1})
	s.connection.Provider = paddle.Provider
	p := &PaddleCheckouts{s}
	called := false
	got, err := p.Checkout(t.Context(), billing.CheckoutRequest{OrganizationID: "org", OfferID: "team", IdempotencyKey: "attempt"}, func(intent string) (billing.Session, error) {
		called = true
		if c.active || intent != id {
			t.Fatal("external write before reservation committed")
		}
		return session, nil
	})
	if err != nil || got != session || !called {
		t.Fatal(got, err, called)
	}
}
func TestPaddleCheckoutRetryCannotDuplicateOrChangeOffer(t *testing.T) {
	for _, mode := range []string{"saved", "uncertain", "other_offer", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			offer := "team"
			var tid, target driver.Value = "txn_01h04vsc0qhwtsbsxh3422wjs4", "https://app.example/checkout?_ptxn=txn_01h04vsc0qhwtsbsxh3422wjs4"
			if mode == "uncertain" {
				tid, target = nil, nil
			}
			if mode == "other_offer" {
				offer = "business"
			}
			steps := []step{{query: "BEGIN"}, {query: "FOR UPDATE", rows: [][]driver.Value{{true}}}, {query: "SELECT offer_id", rows: [][]driver.Value{{offer, tid, target}}}}
			if mode == "blocked" {
				steps[2].rows = nil
				steps = append(steps, step{query: "SELECT EXISTS", rows: [][]driver.Value{{true}}})
			}
			end := "ROLLBACK"
			if mode == "saved" {
				end = "COMMIT"
			}
			steps = append(steps, step{query: end})
			s, _ := scripted(t, steps...)
			s.connection.Provider = paddle.Provider
			got, err := (&PaddleCheckouts{s}).Checkout(t.Context(), billing.CheckoutRequest{OrganizationID: "org", OfferID: "team", IdempotencyKey: "attempt"}, func(string) (billing.Session, error) {
				t.Fatal("duplicate provider call")
				return billing.Session{}, nil
			})
			if mode == "saved" {
				if err != nil || got.ExternalReference != tid {
					t.Fatal(got, err)
				}
			} else if err == nil {
				t.Fatal("unsafe retry accepted")
			}
		})
	}
}
func TestPaddleCheckoutAmbiguousProviderFailureKeepsReservation(t *testing.T) {
	s, c := scripted(t, step{query: "BEGIN"}, step{query: "FOR UPDATE", rows: [][]driver.Value{{true}}}, step{query: "SELECT offer_id"}, step{query: "SELECT EXISTS", rows: [][]driver.Value{{false}}}, step{query: "INSERT INTO paddle_checkouts", affected: 1}, step{query: "COMMIT"})
	s.connection.Provider = paddle.Provider
	_, err := (&PaddleCheckouts{s}).Checkout(t.Context(), billing.CheckoutRequest{OrganizationID: "org", OfferID: "team", IdempotencyKey: "attempt"}, func(string) (billing.Session, error) {
		if c.active {
			t.Fatal("uncommitted")
		}
		return billing.Session{}, billing.ErrProvider
	})
	if !errors.Is(err, billing.ErrProvider) {
		t.Fatal(err)
	}
}
func TestPaddleCustomerBindingIsAtomicAndCannotBeReassigned(t *testing.T) {
	for _, mode := range []string{"new", "existing", "other_customer", "subscription_conflict"} {
		t.Run(mode, func(t *testing.T) {
			steps := []step{{query: "BEGIN"}, {query: "FOR UPDATE", rows: [][]driver.Value{{true}}}, {query: "SELECT customer_reference"}}
			customer := "ctm_01h04vsc0qhwtsbsxh3422wjs4"
			if mode == "new" {
				steps = append(steps, step{query: "INSERT INTO billing_customers", affected: 1})
			} else {
				ref := customer
				if mode == "other_customer" {
					ref = "other"
				}
				steps[2].rows = [][]driver.Value{{ref}}
			}
			if mode != "other_customer" {
				affected := int64(1)
				if mode == "subscription_conflict" {
					affected = 0
				}
				steps = append(steps, step{query: "UPDATE paddle_checkouts", affected: affected})
			}
			end := "COMMIT"
			if mode == "other_customer" || mode == "subscription_conflict" {
				end = "ROLLBACK"
			}
			steps = append(steps, step{query: end})
			s, _ := scripted(t, steps...)
			s.connection.Provider = paddle.Provider
			err := (&PaddleCheckouts{s}).Bind(context.Background(), paddle.Intent{ID: "intent", OrganizationID: "org", TransactionID: "txn_01h04vsc0qhwtsbsxh3422wjs4"}, customer, "sub_01h04vsc0qhwtsbsxh3422wjs4")
			if (err != nil) != (end == "ROLLBACK") {
				t.Fatal(err)
			}
		})
	}
}
