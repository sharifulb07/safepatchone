package quotastore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

// Scripted database/sql transport verifies adapter transactions, scoped binds,
// rollback and errors. It does not emulate PostgreSQL locking or SQL semantics;
// native migration assertions are separately opt-in in migrationtest.
type sqlStep struct {
	contains string
	args     []driver.Value
	rows     [][]driver.Value
	err      error
}
type scriptConn struct {
	t                     *testing.T
	steps                 []sqlStep
	committed, rolledBack bool
	commitError           error
}

func (c *scriptConn) step(query string, args []driver.NamedValue) sqlStep {
	c.t.Helper()
	if len(c.steps) == 0 {
		c.t.Fatalf("unexpected SQL: %s", query)
	}
	s := c.steps[0]
	c.steps = c.steps[1:]
	if !strings.Contains(query, s.contains) {
		c.t.Fatalf("expected %q, got %q", s.contains, query)
	}
	values := make([]driver.Value, len(args))
	for i, a := range args {
		values[i] = a.Value
	}
	if len(values) != 0 || len(s.args) != 0 {
		if !reflect.DeepEqual(values, s.args) {
			c.t.Fatalf("wrong scoped binds: %v != %v", values, s.args)
		}
	}
	return s
}
func (c *scriptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *scriptConn) Close() error              { return nil }
func (c *scriptConn) Begin() (driver.Tx, error) { return nil, errors.New("must specify isolation") }
func (c *scriptConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if opts.Isolation != driver.IsolationLevel(sql.LevelReadCommitted) || opts.ReadOnly {
		c.t.Fatal("wrong isolation", opts)
	}
	s := c.step("BEGIN", nil)
	if s.err != nil {
		return nil, s.err
	}
	return c, nil
}
func (c *scriptConn) Commit() error   { c.committed = true; return c.commitError }
func (c *scriptConn) Rollback() error { c.rolledBack = true; return nil }
func (c *scriptConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	s := c.step(query, args)
	return driver.RowsAffected(1), s.err
}
func (c *scriptConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.step(query, args)
	if s.err != nil {
		return nil, s.err
	}
	return &scriptRows{rows: s.rows}, nil
}

type scriptRows struct{ rows [][]driver.Value }

func (r *scriptRows) Columns() []string {
	if len(r.rows) == 0 {
		return []string{"empty"}
	}
	return make([]string, len(r.rows[0]))
}
func (r *scriptRows) Close() error { return nil }
func (r *scriptRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

type scriptConnector struct{ conn *scriptConn }

func (c scriptConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c scriptConnector) Driver() driver.Driver                        { return scriptDriver{} }

type scriptDriver struct{}

func (scriptDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

func lockSteps() []sqlStep {
	return []sqlStep{
		{contains: "BEGIN"},
		{contains: "INSERT INTO quota_accounts", args: []driver.Value{"org_a"}},
		{contains: "WHERE organization_id = $1 FOR UPDATE", args: []driver.Value{"org_a"}, rows: [][]driver.Value{{"org_a"}}},
	}
}
func scriptStore(t *testing.T, steps []sqlStep) (*Postgres, *scriptConn) {
	t.Helper()
	conn := &scriptConn{t: t, steps: steps}
	db := sql.OpenDB(scriptConnector{conn})
	t.Cleanup(func() {
		_ = db.Close()
		if len(conn.steps) != 0 {
			t.Errorf("unexecuted SQL: %v", conn.steps)
		}
	})
	store, err := NewPostgres(db)
	if err != nil {
		t.Fatal(err)
	}
	return store, conn
}

func TestPostgresReserveBindsTenantAndCommitsAtomically(t *testing.T) {
	steps := append(lockSteps(), []sqlStep{
		{contains: "WHERE organization_id = $1 AND reservation_id = $2", args: []driver.Value{"org_a", "job"}},
		{contains: "FROM entitlement_limits WHERE organization_id = $1 AND limit_key = $2", args: []driver.Value{"org_a", "repositories"}, rows: [][]driver.Value{{"INTEGER", int64(2)}}},
		{contains: "FROM quota_usage WHERE organization_id = $1 AND limit_key = $2 AND period = $3", args: []driver.Value{"org_a", "repositories", "lifetime"}},
		{contains: "INSERT INTO quota_usage", args: []driver.Value{"org_a", "repositories", "lifetime", int64(1), int64(0)}},
		{contains: "INSERT INTO quota_reservations", args: []driver.Value{"org_a", "job", "repositories", "lifetime", int64(1), "RESERVED"}},
	}...)
	store, conn := scriptStore(t, steps)
	service, _ := entitlement.NewQuotaService(store, time.Now)
	r, err := service.Reserve(t.Context(), "org_a", domain.LimitRepositories, 1, "job")
	if err != nil || r.State != entitlement.Reserved || !conn.committed || conn.rolledBack {
		t.Fatal(r, err, conn)
	}
}

func TestPostgresSettlementKeepsOriginalPeriod(t *testing.T) {
	for _, target := range []entitlement.ReservationState{entitlement.Committed, entitlement.Released} {
		t.Run(string(target), func(t *testing.T) {
			consumed := int64(0)
			if target == entitlement.Committed {
				consumed = 2
			}
			steps := append(lockSteps(), []sqlStep{
				{contains: "WHERE organization_id = $1 AND reservation_id = $2", args: []driver.Value{"org_a", "job"}, rows: [][]driver.Value{{"proof_credits_monthly", "2026-08", int64(2), "RESERVED"}}},
				{contains: "FROM quota_usage WHERE organization_id = $1 AND limit_key = $2 AND period = $3", args: []driver.Value{"org_a", "proof_credits_monthly", "2026-08"}, rows: [][]driver.Value{{int64(2), int64(0)}}},
				{contains: "INSERT INTO quota_usage", args: []driver.Value{"org_a", "proof_credits_monthly", "2026-08", int64(0), consumed}},
				{contains: "INSERT INTO quota_reservations", args: []driver.Value{"org_a", "job", "proof_credits_monthly", "2026-08", int64(2), string(target)}},
			}...)
			store, conn := scriptStore(t, steps)
			service, _ := entitlement.NewQuotaService(store, time.Now)
			var r entitlement.Reservation
			var err error
			if target == entitlement.Committed {
				r, err = service.Commit(t.Context(), "org_a", "job")
			} else {
				r, err = service.Release(t.Context(), "org_a", "job")
			}
			if err != nil || r.State != target || !conn.committed {
				t.Fatal(r, err)
			}
		})
	}
}

func TestPostgresPolicyUpdateUsesAdmissionLock(t *testing.T) {
	steps := append(lockSteps(), sqlStep{contains: "INSERT INTO entitlement_limits", args: []driver.Value{"org_a", "repositories", "DISABLED", int64(0)}})
	store, conn := scriptStore(t, steps)
	if err := store.SetLimit(t.Context(), "org_a", domain.EntitlementLimit{Key: domain.LimitRepositories, State: domain.Disabled}); err != nil || !conn.committed {
		t.Fatal(err)
	}
}

func TestPostgresFailuresRollBackAndFailClosed(t *testing.T) {
	failure := errors.New("database failure")
	for _, stage := range []string{"begin", "account", "lock", "missing_policy", "policy_error", "callback", "commit", "partial_write"} {
		t.Run(stage, func(t *testing.T) {
			steps := lockSteps()
			switch stage {
			case "begin":
				steps = steps[:1]
				steps[0].err = failure
			case "account":
				steps = steps[:2]
				steps[1].err = failure
			case "lock":
				steps[2].err = failure
			case "missing_policy", "policy_error":
				step := sqlStep{contains: "FROM entitlement_limits", args: []driver.Value{"org_a", "repositories"}}
				if stage == "policy_error" {
					step.err = failure
				}
				steps = append(steps, step)
			case "partial_write":
				steps = append(steps,
					sqlStep{contains: "INSERT INTO quota_usage", args: []driver.Value{"org_a", "repositories", "lifetime", int64(1), int64(0)}},
					sqlStep{contains: "INSERT INTO quota_reservations", args: []driver.Value{"org_a", "job", "repositories", "lifetime", int64(1), "RESERVED"}, err: failure})
			}
			store, conn := scriptStore(t, steps)
			if stage == "commit" {
				conn.commitError = failure
			}
			var err error
			if stage == "missing_policy" || stage == "policy_error" {
				service, _ := entitlement.NewQuotaService(store, time.Now)
				err = service.Check(t.Context(), "org_a", domain.LimitRepositories, 1)
			} else {
				err = store.WithinOrganization(t.Context(), "org_a", func(tx entitlement.QuotaTransaction) error {
					if stage == "callback" {
						return failure
					}
					if stage == "partial_write" {
						if err := tx.PutUsage(domain.LimitRepositories, "lifetime", entitlement.Usage{Reserved: 1}); err != nil {
							return err
						}
						return tx.PutReservation(entitlement.Reservation{OrganizationID: "org_a", ID: "job", Key: domain.LimitRepositories, Period: "lifetime", Amount: 1, State: entitlement.Reserved})
					}
					return nil
				})
			}
			want := failure
			if stage == "missing_policy" {
				want = entitlement.ErrQuotaDisabled
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if stage != "begin" && stage != "commit" && (!conn.rolledBack || conn.committed) {
				t.Fatal("failed transaction not rolled back")
			}
		})
	}
}
