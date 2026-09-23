package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLicenseConnectivityMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000015_license_connectivity.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"REFERENCES organizations(organization_id)", "NEW.revision <> OLD.revision + 1", "NEW.confirmed_at < OLD.confirmed_at", "NEW.organization_id <> OLD.organization_id", "license_installations_no_delete", "license_installations_no_truncate", "BETWEEN 1 AND 262144"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing invariant", required)
		}
	}
	for _, forbidden := range []string{"UPDATE subscriptions", "INSERT INTO entitlements", "UPDATE edition_limits"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("commercial state changed")
		}
	}
}
func TestPostgresLicenseConnectivityMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000015_license_connectivity.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "license_connectivity.sql", "42P07", "license_connectivity_before.sql", "")
}
