package migrationtest

import (
	"path/filepath"
	"testing"
)

func TestPostgresEvidenceQuorumMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
		if filepath.Base(f) == "000021_evidence_quorum.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "evidence_quorum.sql", "23505", "", "")
}
