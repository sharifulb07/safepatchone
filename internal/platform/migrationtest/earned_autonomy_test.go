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

func TestEarnedAutonomyMigrationParity(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join(root(t), "migrations", "000020_earned_autonomy.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root(t), "internal", "config", "earned_autonomy_bundles.json"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)\$configuration\$\s*(.*?)\s*\$configuration\$`).FindSubmatch(sql)
	if len(match) != 2 {
		t.Fatal("missing configuration")
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(match[1], &b) != nil {
		t.Fatal("invalid configuration")
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		t.Fatal("migration configuration drift")
	}
	for _, fragment := range []string{"('feat_0045', 'agent.earned_autonomy')", "ADD CONSTRAINT agent_earned_binding", "COALESCE(", "AND sealed", "SET sealed=true", "BEGIN;", "COMMIT;"} {
		if !strings.Contains(string(sql), fragment) {
			t.Fatal(fragment)
		}
	}
	for _, fragment := range []string{"UPDATE subscriptions", "UPDATE entitlements", "UPDATE agent_safe_leases", "DELETE FROM"} {
		if strings.Contains(string(sql), fragment) {
			t.Fatal("historical data mutation")
		}
	}
}
func TestPostgresEarnedAutonomyMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
		if filepath.Base(f) == "000020_earned_autonomy.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "earned_autonomy.sql", "23505", "earned_autonomy_before.sql", "")
}
