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

func TestGlobalPatchMigrationParityAndPreservation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000018_global_patch.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)\$configuration\$\s*(.*?)\s*\$configuration\$`).FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatal("missing configuration")
	}
	config, err := os.ReadFile(filepath.Join(root(t), "internal", "config", "global_patch_bundles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(match[1], &a) != nil || json.Unmarshal(config, &b) != nil {
		t.Fatal("invalid JSON")
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		t.Fatal("configuration drift")
	}
	for _, s := range []string{"('feat_0043', 'graph.global_change')", "AND sealed", "FROM edition_features f", "FROM edition_limits WHERE edition_version_id=base", "SET sealed=true", "BEGIN;", "COMMIT;"} {
		if !strings.Contains(string(raw), s) {
			t.Fatal("missing", s)
		}
	}
	for _, s := range []string{"UPDATE subscriptions", "UPDATE entitlements", "UPDATE quota_usage", "UPDATE entitlement_limits", "DELETE FROM", "ON CONFLICT"} {
		if strings.Contains(string(raw), s) {
			t.Fatal("customer/history mutation", s)
		}
	}
}
func TestPostgresGlobalPatchMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
		if filepath.Base(f) == "000018_global_patch.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "global_patch.sql", "23505", "global_patch_before.sql", "")
}
