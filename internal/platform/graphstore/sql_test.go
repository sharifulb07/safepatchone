package graphstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Scripted database/sql transport verifies adapter transactions, scoped binds,
// rollback and errors. It does not emulate PostgreSQL locking or SQL semantics;
// native migration assertions are separately opt-in in migrationtest.
type sqlStep struct {
	contains  string
	args      []driver.Value
	rows      [][]driver.Value
	err       error
	nextError error
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
	return &scriptRows{rows: s.rows, nextError: s.nextError}, nil
}

type scriptRows struct {
	rows      [][]driver.Value
	nextError error
}

func (r *scriptRows) Columns() []string {
	if len(r.rows) == 0 {
		return []string{"empty"}
	}
	return make([]string, len(r.rows[0]))
}
func (r *scriptRows) Close() error { return nil }
func (r *scriptRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		if r.nextError != nil {
			return r.nextError
		}
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
