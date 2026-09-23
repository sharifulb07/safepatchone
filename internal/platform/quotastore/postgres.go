package quotastore

import (
	"context"
	"database/sql"
	"errors"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

// Postgres uses the shared migration schema and an injected database/sql pool.
// Composition owns driver registration, TLS, credentials and a trusted fixed
// search_path. Use the primary database. All policy/accounting writers must use
// this adapter's organization lock; table access must be limited to trusted code.
type Postgres struct{ db *sql.DB }

func NewPostgres(db *sql.DB) (*Postgres, error) {
	if db == nil {
		return nil, entitlement.ErrInvalidQuota
	}
	return &Postgres{db: db}, nil
}

var _ entitlement.QuotaStore = (*Postgres)(nil)

func (s *Postgres) WithinOrganization(ctx context.Context, org string, fn func(entitlement.QuotaTransaction) error) error {
	if s == nil || s.db == nil || ctx == nil || !domain.ValidID(org) || fn == nil {
		return entitlement.ErrInvalidQuota
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Concurrent first use is serialized by the unique account key, then the row
	// lock. Read committed ensures policy reads after waiting see the new policy.
	if _, err := tx.ExecContext(ctx, `INSERT INTO quota_accounts (organization_id) VALUES ($1) ON CONFLICT DO NOTHING`, org); err != nil {
		return err
	}
	var locked string
	if err := tx.QueryRowContext(ctx, `SELECT organization_id FROM quota_accounts WHERE organization_id = $1 FOR UPDATE`, org).Scan(&locked); err != nil {
		return err
	}
	if locked != org {
		return entitlement.ErrQuotaData
	}
	if err := fn(&postgresTx{ctx: ctx, tx: tx, org: org}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// SetLimit provisions resolved policy under the same lock used for admission.
// Caller authorization and source resolution precede this trusted write. No
// edition name, grant metadata, or legacy nullable feature limit is interpreted.
func (s *Postgres) SetLimit(ctx context.Context, org string, l domain.EntitlementLimit) error {
	if err := l.Validate(); err != nil {
		return err
	}
	return s.WithinOrganization(ctx, org, func(transaction entitlement.QuotaTransaction) error {
		t := transaction.(*postgresTx)
		_, err := t.tx.ExecContext(ctx, `INSERT INTO entitlement_limits (organization_id, limit_key, state, value) VALUES ($1,$2,$3,$4)
		ON CONFLICT (organization_id, limit_key) DO UPDATE SET state = EXCLUDED.state, value = EXCLUDED.value`, org, l.Key, l.State, l.Value)
		return err
	})
}

type postgresTx struct {
	ctx context.Context
	tx  *sql.Tx
	org string
}

func (t *postgresTx) Limit(key domain.LimitKey) (domain.EntitlementLimit, error) {
	l := domain.EntitlementLimit{Key: key}
	err := t.tx.QueryRowContext(t.ctx, `SELECT state, value FROM entitlement_limits WHERE organization_id = $1 AND limit_key = $2`, t.org, key).Scan(&l.State, &l.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EntitlementLimit{Key: key, State: domain.Disabled}, nil
	}
	return l, err
}
func (t *postgresTx) Usage(key domain.LimitKey, period string) (entitlement.Usage, error) {
	var u entitlement.Usage
	err := t.tx.QueryRowContext(t.ctx, `SELECT reserved, committed FROM quota_usage WHERE organization_id = $1 AND limit_key = $2 AND period = $3`, t.org, key, period).Scan(&u.Reserved, &u.Committed)
	if errors.Is(err, sql.ErrNoRows) {
		return entitlement.Usage{}, nil
	}
	return u, err
}
func (t *postgresTx) Find(id string) (entitlement.Reservation, error) {
	r := entitlement.Reservation{OrganizationID: t.org, ID: id}
	err := t.tx.QueryRowContext(t.ctx, `SELECT limit_key, period, amount, state FROM quota_reservations WHERE organization_id = $1 AND reservation_id = $2`, t.org, id).Scan(&r.Key, &r.Period, &r.Amount, &r.State)
	if errors.Is(err, sql.ErrNoRows) {
		return entitlement.Reservation{}, entitlement.ErrQuotaNotFound
	}
	return r, err
}
func (t *postgresTx) PutUsage(key domain.LimitKey, period string, u entitlement.Usage) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO quota_usage (organization_id, limit_key, period, reserved, committed) VALUES ($1,$2,$3,$4,$5)
	ON CONFLICT (organization_id, limit_key, period) DO UPDATE SET reserved = EXCLUDED.reserved, committed = EXCLUDED.committed`, t.org, key, period, u.Reserved, u.Committed)
	return err
}
func (t *postgresTx) PutReservation(r entitlement.Reservation) error {
	if r.OrganizationID != t.org {
		return entitlement.ErrQuotaData
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO quota_reservations (organization_id, reservation_id, limit_key, period, amount, state) VALUES ($1,$2,$3,$4,$5,$6)
	ON CONFLICT (organization_id, reservation_id) DO UPDATE SET state = EXCLUDED.state`, t.org, r.ID, r.Key, r.Period, r.Amount, r.State)
	return err
}
