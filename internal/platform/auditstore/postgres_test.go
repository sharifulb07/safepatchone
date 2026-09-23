package auditstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"safepatch/internal/audit"
	"safepatch/internal/authorization"
)

type testConn struct {
	t      *testing.T
	values [][]driver.Value
	err    error
	calls  int
}

func (c *testConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (c *testConn) Close() error                        { return nil }
func (c *testConn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (c *testConn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.calls++
	if !strings.Contains(q, "INSERT INTO audit_events") || len(args) != 3 || args[0].Value != "org" {
		c.t.Fatal("unscoped append")
	}
	return driver.RowsAffected(1), c.err
}
func (c *testConn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.calls++
	if !strings.Contains(q, "WHERE organization_id=$1") || !strings.Contains(q, "LIMIT $3") || len(args) != 3 || args[0].Value != "org" {
		c.t.Fatal("unscoped/unbounded query")
	}
	if c.err != nil {
		return nil, c.err
	}
	return &testRows{c.values}, nil
}

type testRows struct{ values [][]driver.Value }

func (r *testRows) Columns() []string { return []string{"event_id", "record"} }
func (r *testRows) Close() error      { return nil }
func (r *testRows) Next(dst []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dst, r.values[0])
	r.values = r.values[1:]
	return nil
}

type connector struct{ c *testConn }

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c connector) Driver() driver.Driver                        { return unusedDriver{} }

type unusedDriver struct{}

func (unusedDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func TestAuditSQLScopePoisoningAndErrorPaths(t *testing.T) {
	e := audit.Event{ID: "event1", OrganizationID: "org", Actor: authorization.Principal{ID: "user", Kind: authorization.User}, Action: "proof.verify", ResourceID: "repo", Outcome: "UNKNOWN", OccurredAt: time.Now().UTC()}
	raw, _ := json.Marshal(e)
	c := &testConn{t: t, values: [][]driver.Value{{e.ID, string(raw)}}}
	db := sql.OpenDB(connector{c})
	defer db.Close()
	store, _ := NewPostgres(db)
	if err := store.Append(t.Context(), "org", e); err != nil {
		t.Fatal(err)
	}
	if p, err := store.List(t.Context(), "org", audit.Query{Limit: 1}); err != nil || len(p.Events) != 1 {
		t.Fatal(p, err)
	}
	e.ID = "event2"
	next, _ := json.Marshal(e)
	c.values = append(c.values, []driver.Value{e.ID, string(next)})
	if p, err := store.List(t.Context(), "org", audit.Query{Limit: 1}); err != nil || len(p.Events) != 1 || p.NextAfterID != "event1" {
		t.Fatal(p, err)
	}
	e.OrganizationID = "other"
	poisoned, _ := json.Marshal(e)
	c.values = [][]driver.Value{{e.ID, string(poisoned)}}
	if _, err := store.List(t.Context(), "org", audit.Query{Limit: 2}); err == nil {
		t.Fatal("cross tenant leak")
	}
	before := c.calls
	if err := store.Append(t.Context(), "org", e); err == nil {
		t.Fatal("cross tenant append")
	}
	if c.calls != before {
		t.Fatal("invalid write touched DB")
	}
	c.err = errors.New("private database credential")
	if _, err := store.List(t.Context(), "org", audit.Query{Limit: 2}); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatal(err)
	}
	e.OrganizationID = "org"
	if err := store.Append(t.Context(), "org", e); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatal(err)
	}
}
