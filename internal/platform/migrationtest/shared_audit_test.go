package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedAuditMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000016_shared_audit.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"REFERENCES organizations(organization_id)", "PRIMARY KEY(organization_id,event_id)", "audit_events_immutable", "audit_events_no_truncate", "BETWEEN 2 AND 8192", "IS NOT DISTINCT FROM organization_id"} {
		if !strings.Contains(string(raw), s) {
			t.Fatal("missing audit invariant", s)
		}
	}
	for _, s := range []string{"UPDATE edition", "UPDATE subscriptions", "INSERT INTO entitlements"} {
		if strings.Contains(string(raw), s) {
			t.Fatal("commercial history modified")
		}
	}
}
func TestPostgresSharedAuditMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000016_shared_audit.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "shared_audit.sql", "42P07", "shared_audit_before.sql", "")
}
