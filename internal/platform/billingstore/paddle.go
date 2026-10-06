package billingstore

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/billing/paddle"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"safepatch/internal/platform/httpapi"
	"safepatch/internal/platform/organizationstore"
	"time"
)

type PaddleCheckouts struct{ store *Postgres }

// A committed reservation precedes the external write. Replaying an uncertain
// write does not create a second transaction: an operator must recover it.
func (p *PaddleCheckouts) Checkout(ctx context.Context, r billing.CheckoutRequest, create func(string) (billing.Session, error)) (billing.Session, error) {
	fail := billing.Session{}
	s := p.store
	if r.Validate() != nil || create == nil {
		return fail, billing.ErrInvalidRequest
	}
	id := billing.StableID(s.connection.ID, r.OrganizationID, r.IdempotencyKey)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fail, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state='ACTIVE' FROM organizations WHERE organization_id=$1 FOR UPDATE`, r.OrganizationID).Scan(&active); err != nil || !active {
		return fail, billing.ErrOwnership
	}
	var offer string
	var tid, target sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT offer_id,transaction_id,checkout_url FROM paddle_checkouts WHERE connection_id=$1 AND checkout_id=$2`, s.connection.ID, id).Scan(&offer, &tid, &target)
	if err == nil {
		if offer != r.OfferID {
			return fail, billing.ErrOwnership
		}
		if !tid.Valid || !target.Valid {
			return fail, billing.ErrProvider
		}
		return billing.Session{ExternalReference: tid.String, URL: target.String}, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fail, err
	}
	// Prevent a second purchase while a subscription or unresolved checkout exists.
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE organization_id=$1 AND state IN ('PENDING','TRIALING','ACTIVE','PAST_DUE')) OR EXISTS(SELECT 1 FROM paddle_checkouts WHERE connection_id=$2 AND organization_id=$1 AND subscription_id IS NULL)`, r.OrganizationID, s.connection.ID).Scan(&exists); err != nil {
		return fail, err
	}
	if exists {
		return fail, org.ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO paddle_checkouts(connection_id,checkout_id,organization_id,offer_id) SELECT connection_id,$2,$3,$4 FROM billing_connections WHERE connection_id=$1 AND provider='paddle' AND account_reference=$5 AND live_mode=$6`, s.connection.ID, id, r.OrganizationID, r.OfferID, s.connection.AccountReference, s.connection.LiveMode)
	if err != nil {
		return fail, err
	}
	if err = exactlyOne(result); err != nil {
		return fail, err
	}
	if err = tx.Commit(); err != nil {
		return fail, err
	}
	session, err := create(id)
	if err != nil {
		return fail, err
	}
	result, err = s.db.ExecContext(ctx, `UPDATE paddle_checkouts SET transaction_id=$3,checkout_url=$4 WHERE connection_id=$1 AND checkout_id=$2 AND transaction_id IS NULL`, s.connection.ID, id, session.ExternalReference, session.URL)
	if err != nil {
		return fail, err
	}
	if err = exactlyOne(result); err != nil {
		return fail, err
	}
	return session, nil
}
func (p *PaddleCheckouts) Lookup(ctx context.Context, id string) (paddle.Intent, error) {
	i := paddle.Intent{ID: id}
	if !org.ValidID(id) {
		return i, billing.ErrOwnership
	}
	s := p.store
	var tid, sub sql.NullString
	err := s.directoryRow(ctx, `SELECT p.organization_id,p.offer_id,p.transaction_id,p.subscription_id FROM paddle_checkouts p JOIN billing_connections c USING(connection_id) WHERE p.connection_id=$1 AND p.checkout_id=$2 AND c.provider='paddle' AND c.account_reference=$3 AND c.live_mode=$4`, s.connection.ID, id, s.connection.AccountReference, s.connection.LiveMode).Scan(&i.OrganizationID, &i.OfferID, &tid, &sub)
	if err != nil {
		return i, err
	}
	if !tid.Valid {
		return i, billing.ErrOwnership
	}
	i.TransactionID, i.SubscriptionID = tid.String, sub.String
	return i, nil
}
func (p *PaddleCheckouts) Bind(ctx context.Context, i paddle.Intent, customer, subscription string) error {
	s := p.store
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state='ACTIVE' FROM organizations WHERE organization_id=$1 FOR UPDATE`, i.OrganizationID).Scan(&active); err != nil || !active {
		return billing.ErrOwnership
	}
	var reference string
	err = tx.QueryRowContext(ctx, `SELECT customer_reference FROM billing_customers WHERE connection_id=$1 AND organization_id=$2`, s.connection.ID, i.OrganizationID).Scan(&reference)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO billing_customers(connection_id,organization_id,customer_reference) VALUES($1,$2,$3)`, s.connection.ID, i.OrganizationID, customer)
		reference = customer
	}
	if err != nil {
		return err
	}
	if reference != customer {
		return billing.ErrOwnership
	}
	result, err := tx.ExecContext(ctx, `UPDATE paddle_checkouts SET subscription_id=$5 WHERE connection_id=$1 AND checkout_id=$2 AND organization_id=$3 AND transaction_id=$4 AND (subscription_id IS NULL OR subscription_id=$5)`, s.connection.ID, i.ID, i.OrganizationID, i.TransactionID, subscription)
	if err != nil {
		return err
	}
	if err = exactlyOne(result); err != nil {
		return err
	}
	return tx.Commit()
}

type PaddleSubscriptions struct {
	Store      *Postgres
	Adapter    *paddle.Adapter
	Management *application.BillingService
	Webhook    http.Handler
}

func NewPaddleSubscriptions(db *sql.DB, c Connection, cfg paddle.Config, features *feature.FeatureCatalog, now func() time.Time) (*PaddleSubscriptions, error) {
	if c.Provider != paddle.Provider || c.LiveMode != (cfg.Environment == "production") {
		return nil, billing.ErrInvalidConfiguration
	}
	s, err := NewPostgres(db, c, features, now)
	if err != nil {
		return nil, err
	}
	cfg.Customers = s
	cfg.Checkouts = &PaddleCheckouts{s}
	cfg.Now = now
	a, err := paddle.New(cfg)
	if err != nil {
		return nil, err
	}
	sync, err := paddle.NewIntegration(a, s)
	if err != nil {
		return nil, err
	}
	organizations, err := organizationstore.NewPostgres(db, features)
	if err != nil {
		return nil, err
	}
	permissions, err := authorization.NewPermissionService(organizations)
	if err != nil {
		return nil, err
	}
	management, err := application.NewBillingService(a, organizations, permissions)
	if err != nil {
		return nil, err
	}
	webhook, err := httpapi.NewBillingWebhookHandlerWithSignature(sync, "Paddle-Signature")
	if err != nil {
		return nil, err
	}
	return &PaddleSubscriptions{s, a, management, webhook}, nil
}
func (s *PaddleSubscriptions) Checkout(ctx context.Context, a authorization.Principal, r billing.CheckoutRequest) (billing.Session, error) {
	return s.Management.CreateCheckout(ctx, a, r)
}
func (s *PaddleSubscriptions) Portal(ctx context.Context, a authorization.Principal, r billing.PortalRequest) (billing.Session, error) {
	return s.Management.CreateCustomerPortal(ctx, a, r)
}
