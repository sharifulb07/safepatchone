package licensestore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"safepatch/internal/licensing"
)

// Scripted driver verifies SQL binding/error paths; native SQL constraints are
// exercised by the opt-in migration suite, not simulated by this driver.
type conn struct {
	t        *testing.T
	err      error
	affected int64
	calls    int
	now      time.Time
}

func (c *conn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (c *conn) Close() error                        { return nil }
func (c *conn) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected transaction") }
func (c *conn) QueryContext(_ context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.calls++
	if !strings.Contains(q, "WHERE organization_id=$1") || len(args) != 1 || args[0].Value != "org" {
		c.t.Fatal("unscoped read")
	}
	if c.err != nil {
		return nil, c.err
	}
	return &rows{values: []driver.Value{int64(7), false, []byte("signed"), c.now, false}}, nil
}
func (c *conn) ExecContext(_ context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.calls++
	if len(args) < 6 || args[0].Value != "org" {
		c.t.Fatal("unscoped write")
	}
	if strings.HasPrefix(q, "UPDATE") {
		if !strings.Contains(q, "AND revision=$7") || args[6].Value != int64(7) || args[1].Value != int64(8) {
			c.t.Fatal("missing CAS")
		}
	} else if !strings.Contains(q, "ON CONFLICT(organization_id) DO NOTHING") {
		c.t.Fatal("unsafe insert")
	}
	return driver.RowsAffected(c.affected), c.err
}

type rows struct{ values []driver.Value }

func (r *rows) Columns() []string {
	return []string{"revision", "revoked", "envelope", "confirmed_at", "refresh_denied"}
}
func (r *rows) Close() error { return nil }
func (r *rows) Next(dst []driver.Value) error {
	if r.values == nil {
		return io.EOF
	}
	copy(dst, r.values)
	r.values = nil
	return nil
}

type connector struct{ c *conn }

func (c connector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c connector) Driver() driver.Driver                        { return unusedDriver{} }

type unusedDriver struct{}

func (unusedDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func TestPersistedAnchorCASIsolationAndErrors(t *testing.T) {
	c := &conn{t: t, affected: 1, now: time.Now().UTC().Truncate(time.Microsecond)}
	db := sql.OpenDB(connector{c})
	defer db.Close()
	s, _ := NewPostgres(db, "org")
	v, err := s.LoadLicense(t.Context(), "org")
	if err != nil || v.Revision != 7 || !v.ConfirmedAt.Equal(c.now) {
		t.Fatal(v, err)
	}
	v.Revision = 8
	if err = s.CompareAndSwapLicense(t.Context(), "org", 7, v); err != nil {
		t.Fatal(err)
	}
	c.affected = 0
	if err = s.CompareAndSwapLicense(t.Context(), "org", 7, v); !errors.Is(err, licensing.ErrLicenseConflict) {
		t.Fatal(err)
	}
	c.affected = 1
	v.Revision = 1
	if err = s.CompareAndSwapLicense(t.Context(), "org", 0, v); err != nil {
		t.Fatal(err)
	}
	before := c.calls
	if _, err = s.LoadLicense(t.Context(), "other"); err == nil {
		t.Fatal("foreign read")
	}
	if err = s.CompareAndSwapLicense(t.Context(), "other", 0, v); err == nil {
		t.Fatal("foreign write")
	}
	if err = s.CompareAndSwapLicense(t.Context(), "org", 0, licensing.LicenseState{}); err == nil {
		t.Fatal("invalid state")
	}
	if c.calls != before {
		t.Fatal("invalid input touched DB")
	}
	c.err = errors.New("private DB credential")
	if _, err = s.LoadLicense(t.Context(), "org"); !errors.Is(err, licensing.ErrLicenseUnavailable) {
		t.Fatal(err)
	}
	if err = s.CompareAndSwapLicense(t.Context(), "org", 0, v); !errors.Is(err, licensing.ErrLicenseUnavailable) {
		t.Fatal(err)
	}
}
