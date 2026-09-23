package organizationstore

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

	"safepatch/internal/domain/feature"
)

// Scripted transport verifies scoped queries and error handling, not PostgreSQL
// constraints or locking. Native SQL tests use the shared migration harness.
type step struct {
	sql       string
	args      []driver.Value
	rows      [][]driver.Value
	affected  int64
	err, late error
}
type connection struct {
	t     *testing.T
	steps []step
}

func (c *connection) next(q string, args []driver.NamedValue) step {
	c.t.Helper()
	if len(c.steps) == 0 {
		c.t.Fatal("unexpected SQL", q)
	}
	s := c.steps[0]
	c.steps = c.steps[1:]
	actual := make([]driver.Value, len(args))
	for i, a := range args {
		actual[i] = a.Value
	}
	if !strings.Contains(q, s.sql) || !reflect.DeepEqual(actual, s.args) {
		c.t.Fatalf("unexpected SQL/binds: %s %v; want %s %v", q, actual, s.sql, s.args)
	}
	return s
}
func (c *connection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *connection) Close() error              { return nil }
func (c *connection) Begin() (driver.Tx, error) { return nil, errors.New("unexpected transaction") }
func (c *connection) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	s := c.next(q, args)
	return driver.RowsAffected(s.affected), s.err
}
func (c *connection) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.next(q, args)
	return &records{rows: s.rows, late: s.late}, s.err
}

type records struct {
	rows [][]driver.Value
	late error
}

func (r *records) Columns() []string {
	if len(r.rows) == 0 {
		return []string{"empty"}
	}
	return make([]string, len(r.rows[0]))
}
func (r *records) Close() error { return nil }
func (r *records) Next(out []driver.Value) error {
	if len(r.rows) == 0 {
		if r.late != nil {
			return r.late
		}
		return io.EOF
	}
	copy(out, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

type connector struct{ c *connection }

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c connector) Driver() driver.Driver                        { return testDriver{} }

type testDriver struct{}

func (testDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func scripted(t *testing.T, steps ...step) *Postgres {
	t.Helper()
	c := &connection{t: t, steps: steps}
	db := sql.OpenDB(connector{c})
	catalog, _ := feature.NewBuiltinCatalog("1.0.0", time.Now())
	s, err := NewPostgres(db, catalog)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		if len(c.steps) != 0 {
			t.Error("unexecuted SQL", c.steps)
		}
	})
	return s
}

type sqlState string

func (s sqlState) Error() string    { return "database detail" }
func (s sqlState) SQLState() string { return string(s) }
