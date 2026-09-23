package billingstore

import (
	"context"
	"database/sql"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
)

// OnboardingCheckout provisions the immutable organization/customer binding and
// then delegates to the existing authorized checkout service.
type OnboardingCheckout struct {
	Subscriptions *StripeSubscriptions
	Permissions   authorization.PermissionChecker
}

func (c *OnboardingCheckout) Checkout(ctx context.Context, actor authorization.Principal, r billing.CheckoutRequest) (billing.Session, error) {
	if c == nil || c.Subscriptions == nil || c.Permissions == nil || r.Validate() != nil || actor.Validate() != nil {
		return billing.Session{}, billing.ErrInvalidRequest
	}
	allowed, err := c.Permissions.Allowed(ctx, actor, r.OrganizationID, authorization.KeyBillingManage)
	if err != nil {
		return billing.Session{}, err
	}
	if !allowed {
		return billing.Session{}, application.ErrPermissionDenied
	}
	s := c.Subscriptions.Store
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return billing.Session{}, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state='ACTIVE' FROM organizations WHERE organization_id=$1 FOR UPDATE`, r.OrganizationID).Scan(&active); err != nil || !active {
		return billing.Session{}, billing.ErrOwnership
	}
	var customer string
	err = tx.QueryRowContext(ctx, `SELECT customer_reference FROM billing_customers WHERE connection_id=$1 AND organization_id=$2`, s.connection.ID, r.OrganizationID).Scan(&customer)
	if errors.Is(err, sql.ErrNoRows) {
		customer, err = c.Subscriptions.Adapter.CreateOrganizationCustomer(ctx, r.OrganizationID)
		if err != nil {
			return billing.Session{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_customers(connection_id,organization_id,customer_reference) VALUES($1,$2,$3)`, s.connection.ID, r.OrganizationID, customer)
	}
	if err != nil {
		return billing.Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return billing.Session{}, err
	}
	return c.Subscriptions.Management.CreateCheckout(ctx, actor, r)
}
