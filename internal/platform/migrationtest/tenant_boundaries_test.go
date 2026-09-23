package migrationtest

import (
	"path/filepath"
	"testing"
)

// Runs against the same schema and immutable migration history as every edition.
func TestPostgresTenantBoundariesAcrossAllEditionPairs(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000016_shared_audit.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "tenant_boundaries.sql", "42P07", "", "")
}
