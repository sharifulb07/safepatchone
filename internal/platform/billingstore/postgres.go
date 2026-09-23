// Package billingstore synchronizes trusted billing observations into the shared
// PostgreSQL domain. Runtime entitlement reads never contact a billing provider.
package billingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
)

type Connection struct {
	ID, Provider, AccountReference string
	LiveMode                       bool
}

func (c Connection) valid() bool {
	return org.ValidID(c.ID) && org.ValidID(c.Provider) && org.ValidID(c.AccountReference)
}

type Postgres struct {
	db         *sql.DB
	connection Connection
	features   *feature.FeatureCatalog
	now        func() time.Time
}

var _ billing.CustomerDirectory = (*Postgres)(nil)
var _ billing.SubscriptionStore = (*Postgres)(nil)

func NewPostgres(db *sql.DB, c Connection, features *feature.FeatureCatalog, now func() time.Time) (*Postgres, error) {
	if db == nil || !c.valid() || features == nil || now == nil {
		return nil, billing.ErrInvalidConfiguration
	}
	return &Postgres{db, c, features, now}, nil
}

type transactionKey struct{}
type transactionScope struct {
	store *Postgres
	tx    *sql.Tx
}

// Directory reads made by the composed adapter during reconciliation share its
// transaction. They cannot exhaust a pool waiting for a second connection.
func (s *Postgres) directoryRow(ctx context.Context, query string, args ...any) *sql.Row {
	if scope, ok := ctx.Value(transactionKey{}).(transactionScope); ok && scope.store == s {
		return scope.tx.QueryRowContext(ctx, query, args...)
	}
	return s.db.QueryRowContext(ctx, query, args...)
}
func (s *Postgres) check(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return billing.ErrInvalidRequest
	}
	return ctx.Err()
}

// RegisterConnection and BindCustomer are trusted provisioning operations, not
// webhook or public API actions. Keys must correspond to this account/environment.
func (s *Postgres) RegisterConnection(ctx context.Context) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	c := s.connection
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_connections(connection_id,provider,account_reference,live_mode) VALUES($1,$2,$3,$4)`, c.ID, c.Provider, c.AccountReference, c.LiveMode)
	return err
}
func (s *Postgres) BindCustomer(ctx context.Context, organizationID, customer string) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if !org.ValidID(organizationID) || !org.ValidID(customer) {
		return billing.ErrInvalidRequest
	}
	c := s.connection
	result, err := s.db.ExecContext(ctx, `INSERT INTO billing_customers(connection_id,organization_id,customer_reference) SELECT connection_id,$2,$3 FROM billing_connections WHERE connection_id=$1 AND provider=$4 AND account_reference=$5 AND live_mode=$6`, c.ID, organizationID, customer, c.Provider, c.AccountReference, c.LiveMode)
	if err != nil {
		return err
	}
	return exactlyOne(result)
}
func exactlyOne(r sql.Result) error {
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return org.ErrConflict
	}
	return nil
}
func (s *Postgres) CustomerForOrganization(ctx context.Context, provider, organizationID string) (string, error) {
	if err := s.check(ctx); err != nil {
		return "", err
	}
	if provider != s.connection.Provider || !org.ValidID(organizationID) {
		return "", billing.ErrOwnership
	}
	c := s.connection
	var id string
	err := s.directoryRow(ctx, `SELECT b.customer_reference FROM billing_customers b JOIN billing_connections c USING(connection_id) WHERE b.connection_id=$1 AND b.organization_id=$2 AND c.provider=$3 AND c.account_reference=$4 AND c.live_mode=$5`, c.ID, organizationID, c.Provider, c.AccountReference, c.LiveMode).Scan(&id)
	if err != nil {
		return "", err
	}
	if !org.ValidID(id) {
		return "", billing.ErrInvalidData
	}
	return id, nil
}
func (s *Postgres) OrganizationForCustomer(ctx context.Context, provider, customer string) (string, error) {
	if err := s.check(ctx); err != nil {
		return "", err
	}
	if provider != s.connection.Provider || !org.ValidID(customer) {
		return "", billing.ErrOwnership
	}
	c := s.connection
	var id string
	err := s.directoryRow(ctx, `SELECT b.organization_id FROM billing_customers b JOIN billing_connections c USING(connection_id) WHERE b.connection_id=$1 AND b.customer_reference=$2 AND c.provider=$3 AND c.account_reference=$4 AND c.live_mode=$5`, c.ID, customer, c.Provider, c.AccountReference, c.LiveMode).Scan(&id)
	if err != nil {
		return "", err
	}
	if !org.ValidID(id) {
		return "", billing.ErrInvalidData
	}
	return id, nil
}
func (s *Postgres) Accept(ctx context.Context, n billing.Notification) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if n.Validate() != nil || n.Provider != s.connection.Provider {
		return billing.ErrInvalidData
	}
	c := s.connection
	_, err := s.db.ExecContext(ctx, `INSERT INTO billing_events(connection_id,event_id,organization_id,external_reference,occurred_at)
 SELECT b.connection_id,$2,b.organization_id,$4,$5 FROM billing_customers b JOIN billing_connections c USING(connection_id)
 WHERE b.connection_id=$1 AND b.organization_id=$3 AND c.provider=$6 AND c.account_reference=$7 AND c.live_mode=$8
 ON CONFLICT(connection_id,event_id) DO NOTHING`, c.ID, n.EventID, n.OrganizationID, n.ExternalReference, n.OccurredAt, c.Provider, c.AccountReference, c.LiveMode)
	if err != nil {
		return err
	}
	var owner, reference string
	var occurred time.Time
	err = s.db.QueryRowContext(ctx, `SELECT organization_id,external_reference,occurred_at FROM billing_events WHERE connection_id=$1 AND event_id=$2`, c.ID, n.EventID).Scan(&owner, &reference, &occurred)
	if err != nil {
		return err
	}
	if owner != n.OrganizationID || reference != n.ExternalReference || !occurred.Equal(n.OccurredAt) {
		return org.ErrConflict
	}
	return nil
}
func (s *Postgres) Pending(ctx context.Context, after string, limit int) ([]string, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if (after != "" && !org.ValidID(after)) || limit < 1 || limit > 200 {
		return nil, billing.ErrInvalidRequest
	}
	rows, err := s.db.QueryContext(ctx, `SELECT event_id FROM billing_events WHERE connection_id=$1 AND applied_at IS NULL AND event_id COLLATE "C">$2 COLLATE "C" ORDER BY event_id COLLATE "C" LIMIT $3`, s.connection.ID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if !org.ValidID(id) || id <= after || len(ids) >= limit {
			return nil, billing.ErrInvalidData
		}
		ids = append(ids, id)
		after = id
	}
	return ids, rows.Err()
}
func (s *Postgres) Reconcile(ctx context.Context, eventID string, reader billing.SubscriptionReader) (bool, error) {
	if err := s.check(ctx); err != nil {
		return false, err
	}
	if !org.ValidID(eventID) || reader == nil {
		return false, billing.ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	c := s.connection
	var owner, reference string
	err = tx.QueryRowContext(ctx, `SELECT e.organization_id,e.external_reference FROM billing_events e
 JOIN billing_customers b ON b.connection_id=e.connection_id AND b.organization_id=e.organization_id
 JOIN billing_connections c ON c.connection_id=e.connection_id JOIN organizations o ON o.organization_id=e.organization_id
 WHERE e.connection_id=$1 AND e.event_id=$2 AND c.provider=$3 AND c.account_reference=$4 AND c.live_mode=$5 FOR UPDATE OF o`, c.ID, eventID, c.Provider, c.AccountReference, c.LiveMode).Scan(&owner, &reference)
	if err != nil {
		return false, err
	}
	if !org.ValidID(owner) || !org.ValidID(reference) {
		return false, billing.ErrInvalidData
	}
	var applied sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT applied_at FROM billing_events WHERE connection_id=$1 AND event_id=$2`, c.ID, eventID).Scan(&applied); err != nil {
		return false, err
	}
	if applied.Valid {
		return true, tx.Commit()
	}
	// Fetch AFTER the per-organization lock, so older deliveries cannot overwrite
	// a newer observation fetched by another worker. No ordering by event timestamp.
	fetchContext := context.WithValue(ctx, transactionKey{}, transactionScope{store: s, tx: tx})
	snapshot, err := reader.ReadSubscription(fetchContext, billing.SubscriptionRequest{OrganizationID: owner, ExternalReference: reference})
	if err != nil {
		return false, err
	}
	if snapshot.OrganizationID != owner || snapshot.ExternalReference != reference || snapshot.Provider != c.Provider {
		return false, billing.ErrOwnership
	}
	if err = s.apply(ctx, tx, eventID, snapshot); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE billing_events SET applied_at=$3 WHERE connection_id=$1 AND event_id=$2 AND applied_at IS NULL`, c.ID, eventID, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return false, err
	}
	if err = exactlyOne(result); err != nil {
		return false, err
	}
	return false, tx.Commit()
}
func (s *Postgres) apply(ctx context.Context, tx *sql.Tx, eventID string, v billing.SubscriptionSnapshot) error {
	at := s.now().UTC().Truncate(time.Microsecond)
	record := org.Subscription{OrganizationID: v.OrganizationID, SubscriptionID: billing.StableID(s.connection.ID, v.ExternalReference), EditionVersionID: v.EditionVersionID, Provider: v.Provider, ExternalReference: v.ExternalReference, State: v.State, Revision: 1, CreatedAt: v.CreatedAt, UpdatedAt: at}
	if record.Validate() != nil || (!v.AccessUntil.IsZero() && !org.ValidTime(v.AccessUntil)) || ((v.State == org.Subscribed || v.State == org.Trialing) && (!org.ValidTime(v.AccessUntil) || !v.AccessUntil.After(v.CreatedAt))) {
		return billing.ErrInvalidData
	}
	// Validate the sealed local configuration before persisting any observation.
	if _, err := s.bundle(ctx, tx, v.EditionVersionID); err != nil {
		return err
	}
	var old org.Subscription
	err := tx.QueryRowContext(ctx, `SELECT organization_id,subscription_id,state,revision,created_at,updated_at FROM subscriptions WHERE provider=$1 AND external_reference=$2`, v.Provider, v.ExternalReference).Scan(&old.OrganizationID, &old.SubscriptionID, &old.State, &old.Revision, &old.CreatedAt, &old.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if old.OrganizationID != v.OrganizationID || !org.ValidID(old.SubscriptionID) || !old.CreatedAt.Equal(v.CreatedAt) || old.UpdatedAt.After(at) || old.Revision < 1 || old.Revision == math.MaxInt64 || !old.State.Valid() {
			return billing.ErrInvalidData
		}
		if (old.State == org.Canceled || old.State == org.Expired) && v.State != old.State {
			return billing.ErrInvalidData
		}
		record.SubscriptionID = old.SubscriptionID
		record.Revision = old.Revision + 1
		result, err := tx.ExecContext(ctx, `UPDATE subscriptions SET edition_version_id=$3,state=$4,revision=$5,updated_at=$6 WHERE organization_id=$1 AND subscription_id=$2 AND revision=$7`, record.OrganizationID, record.SubscriptionID, record.EditionVersionID, string(record.State), record.Revision, at, old.Revision)
		if err != nil {
			return err
		}
		if err = exactlyOne(result); err != nil {
			return err
		}
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO subscriptions(organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, record.OrganizationID, record.SubscriptionID, record.EditionVersionID, record.Provider, record.ExternalReference, string(record.State), record.Revision, record.CreatedAt, at)
		if err != nil {
			return err
		}
	}
	var until any
	if !v.AccessUntil.IsZero() {
		until = v.AccessUntil.UTC()
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO billing_subscription_state(organization_id,subscription_id,connection_id,revision,access_until,payment_settled,cancel_at_period_end) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(organization_id,subscription_id) DO UPDATE SET revision=EXCLUDED.revision,access_until=EXCLUDED.access_until,payment_settled=EXCLUDED.payment_settled,cancel_at_period_end=EXCLUDED.cancel_at_period_end WHERE billing_subscription_state.connection_id=EXCLUDED.connection_id`, record.OrganizationID, record.SubscriptionID, s.connection.ID, record.Revision, until, v.PaymentSettled, v.CancelAtPeriodEnd)
	if err != nil {
		return err
	}
	if err = exactlyOne(result); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Subscription org.Subscription
		Observation  billing.SubscriptionSnapshot
	}{record, v})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO billing_subscription_history(organization_id,subscription_id,revision,connection_id,event_id,canonical_snapshot) VALUES($1,$2,$3,$4,$5,$6)`, record.OrganizationID, record.SubscriptionID, record.Revision, s.connection.ID, eventID, string(raw))
	return err
}
