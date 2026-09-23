package migrationtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain/feature"
)

func root(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no module root")
		}
		dir = parent
	}
}

func TestMigrationFeatureIdentityContract(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, regexp.MustCompile(`\('(feat_[0-9]+)', '([^']+)'\)`).FindAllStringSubmatch(string(data), -1)...)
	}
	fc, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != fc.Len() {
		t.Fatalf("SQL has %d identities; domain has %d", len(rows), fc.Len())
	}
	seen := map[string]bool{}
	for _, row := range rows {
		f, err := fc.GetByID(row[1])
		if err != nil || string(f.Key) != row[2] || seen[row[1]] {
			t.Fatalf("SQL identity mismatch: %v", row)
		}
		seen[row[1]] = true
	}
}

// Uses ordinary PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD or PGSERVICE settings.
// Opt in explicitly; the test creates and removes only its own unique schema.
func TestPostgresEditionMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql"}, "editions.sql", "42P07", "", "")
}

func TestPostgresInitialBundlesMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql"}, "initial_bundles.sql", "23505", "initial_bundles_before.sql", "")
}

func TestPostgresInitialBundleConflict(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql"}, "initial_bundles_conflict.sql", "", "initial_bundles_conflict_before.sql", "23505")
}

func testPostgresMigrations(t *testing.T, migrationNames []string, assertionFile, reapplyState, beforeLatest, applyState string) {
	t.Helper()
	if os.Getenv("SAFEPATCH_POSTGRES_MIGRATION_TEST") != "1" {
		t.Skip("native PostgreSQL integration requires SAFEPATCH_POSTGRES_MIGRATION_TEST=1 and libpq connection settings")
	}
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Fatal("psql required for opted-in PostgreSQL test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	schema := fmt.Sprintf("safepatch_editions_test_%d", time.Now().UnixNano())
	run := func(sql string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, psql, "-X", "--no-password", "--set=ON_ERROR_STOP=1", "--set=VERBOSITY=verbose", "--quiet")
		cmd.Stdin = strings.NewReader(sql)
		return cmd.CombinedOutput()
	}
	if out, err := run("CREATE SCHEMA " + schema + ";"); err != nil {
		t.Fatalf("create test schema: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cleanupCtx, psql, "-X", "--no-password", "--set=ON_ERROR_STOP=1", "--quiet")
		cmd.Stdin = strings.NewReader("DROP SCHEMA " + schema + " CASCADE;")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("cleanup test schema: %v\n%s", err, out)
		}
	})
	var migrations strings.Builder
	var lastMigration []byte
	for i, name := range migrationNames {
		if beforeLatest != "" && i == len(migrationNames)-1 {
			setup, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", beforeLatest))
			if err != nil {
				t.Fatal(err)
			}
			migrations.Write(setup)
			migrations.WriteByte('\n')
		}
		lastMigration, err = os.ReadFile(filepath.Join(root(t), "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		migrations.Write(lastMigration)
		migrations.WriteByte('\n')
	}
	assertions, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", assertionFile))
	if err != nil {
		t.Fatal(err)
	}
	if applyState != "" {
		if out, err := run("SET search_path TO " + schema + ", pg_catalog;\n" + migrations.String()); err == nil || !strings.Contains(string(out), applyState) {
			t.Fatalf("expected failed seed SQLSTATE %s: %v\n%s", applyState, err, out)
		}
		// A fresh connection proves the failed transaction left no partial seed.
		if out, err := run("SET search_path TO " + schema + ", pg_catalog;\n" + string(assertions)); err != nil {
			t.Fatalf("failed-seed rollback assertions: %v\n%s", err, out)
		}
		return
	}
	if out, err := run("SET search_path TO " + schema + ", pg_catalog;\n" + migrations.String() + "\n" + string(assertions)); err != nil {
		t.Fatalf("migration assertions: %v\n%s", err, out)
	}
	// Reapplying the immutable migration must fail instead of replacing history.
	if out, err := run("SET search_path TO " + schema + ", pg_catalog;\n" + string(lastMigration)); err == nil || !strings.Contains(string(out), reapplyState) {
		t.Fatalf("expected reapplication SQLSTATE %s: %v\n%s", reapplyState, err, out)
	}
}
