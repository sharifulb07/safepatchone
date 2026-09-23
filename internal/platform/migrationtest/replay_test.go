package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000014_replay.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"('dataset_retention_days', 'CEILING')", "('replay_concurrency', 'CAPACITY')", "quota_usage_resource_period", "REFERENCES repositories(organization_id,repository_id)", "REFERENCES replay_datasets(organization_id,repository_id,digest)", "PRIMARY KEY(organization_id,repository_id,run_id)", "OLD.result IS NOT NULL", "result IS NOT NULL OR decision='UNKNOWN'", "replay_datasets_no_truncate", "replay_runs_no_truncate"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing replay invariant", required)
		}
	}
	for _, forbidden := range []string{"UPDATE subscriptions", "UPDATE edition_limits", "INSERT INTO entitlements"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("commercial history changed", forbidden)
		}
	}
}
func TestPostgresReplayMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000014_replay.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "replay.sql", "23505", "replay_before.sql", "")
}
