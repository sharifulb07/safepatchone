package migrationtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestCompositionMigrationConfigurationParityAndNoReassignment(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000017_composition_guard.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)\$configuration\$\s*(.*?)\s*\$configuration\$`).FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatal("configuration missing")
	}
	compiled, err := os.ReadFile(filepath.Join(root(t), "internal", "config", "composition_bundles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(match[1], &a) != nil || json.Unmarshal(compiled, &b) != nil {
		t.Fatal("configuration invalid")
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		t.Fatal("SQL/Go bundle drift")
	}
	for _, required := range []string{"('feat_0042', 'proof.composition')", "FROM edition_features f", "FROM edition_limits WHERE edition_version_id=base", "SET sealed=true", "AND sealed", "BEGIN;", "COMMIT;"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal(required)
		}
	}
	for _, forbidden := range []string{"UPDATE subscriptions", "UPDATE entitlements", "UPDATE entitlement_limits", "UPDATE quota_usage", "DELETE FROM", "ON CONFLICT"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("history/assignment mutation", forbidden)
		}
	}
}
func TestPostgresCompositionGuardMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000017_composition_guard.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "composition_guard.sql", "23505", "composition_guard_before.sql", "")
}
