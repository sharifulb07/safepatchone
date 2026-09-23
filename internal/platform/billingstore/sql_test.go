package billingstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"safepatch/internal/domain/feature"
	"strings"
	"testing"
	"time"
)

// Scripted transport exercises transactions and scope, not PostgreSQL locking.
type step struct {
	query    string
	args     []driver.Value
	rows     [][]driver.Value
	err      error
	check    func([]driver.NamedValue)
	affected int64
}
type conn struct {
	t      *testing.T
	steps  []step
	active bool
}

func (c *conn) next(q string, a []driver.NamedValue) step {
	c.t.Helper()
	if len(c.steps) == 0 {
		c.t.Fatal("unexpected SQL", q)
	}
	s := c.steps[0]
	c.steps = c.steps[1:]
	if !strings.Contains(q, s.query) {
		c.t.Fatalf("got SQL %s, want %s", q, s.query)
	}
	if s.args != nil {
		values := make([]driver.Value, len(a))
		for i, v := range a {
			values[i] = v.Value
		}
		if !reflect.DeepEqual(values, s.args) {
			c.t.Fatalf("unexpected binds: %v want %v", values, s.args)
		}
	}
	if s.check != nil {
		s.check(a)
	}
	return s
}
func (c *conn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (c *conn) Close() error                        { return nil }
func (c *conn) Begin() (driver.Tx, error)           { return c.BeginTx(context.Background(), driver.TxOptions{}) }
func (c *conn) BeginTx(_ context.Context, o driver.TxOptions) (driver.Tx, error) {
	s := c.next("BEGIN", []driver.NamedValue{{Value: int64(o.Isolation)}, {Value: o.ReadOnly}})
	if s.err != nil {
		return nil, s.err
	}
	c.active = true
	return c, nil
}
func (c *conn) Commit() error   { c.active = false; return c.next("COMMIT", nil).err }
func (c *conn) Rollback() error { c.active = false; return c.next("ROLLBACK", nil).err }
func (c *conn) ExecContext(_ context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	s := c.next(q, a)
	return driver.RowsAffected(s.affected), s.err
}
func (c *conn) QueryContext(_ context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	s := c.next(q, a)
	return &records{rows: s.rows}, s.err
}

type records struct{ rows [][]driver.Value }

func (r *records) Columns() []string {
	if len(r.rows) == 0 {
		return []string{"empty"}
	}
	return make([]string, len(r.rows[0]))
}
func (r *records) Close() error { return nil }
func (r *records) Next(out []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(out, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

type connector struct{ c *conn }

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c connector) Driver() driver.Driver                        { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func stamp() time.Time                              { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
func scripted(t *testing.T, steps ...step) (*Postgres, *conn) {
	t.Helper()
	c := &conn{t: t, steps: steps}
	db := sql.OpenDB(connector{c})
	features, _ := feature.NewBuiltinCatalog("1.0.0", stamp())
	s, err := NewPostgres(db, Connection{ID: "conn", Provider: "stripe", AccountReference: "acct_test"}, features, stamp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if len(c.steps) != 0 || c.active {
			t.Error("unexecuted SQL or open transaction", c.steps, c.active)
		}
	})
	return s, c
}
func bundleSteps(version, key string) []step {
	return []step{
		{query: "FROM edition_versions", args: []driver.Value{version}, rows: [][]driver.Value{{version, key, stamp().Add(-24 * time.Hour), true}}},
		{query: "FROM edition_features", args: []driver.Value{version}, rows: [][]driver.Value{{"feat_0011", true, int64(5), "{}"}}},
	}
}
