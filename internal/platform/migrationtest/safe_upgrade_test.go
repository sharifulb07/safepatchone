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

func TestSafeUpgradeMigrationConfigurationParity(t *testing.T) {
	sql, err := os.ReadFile(filepath.Join(root(t), "migrations", "000019_safe_upgrade.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root(t), "internal", "config", "safe_upgrade_bundles.json"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`(?s)\$configuration\$\s*(.*?)\s*\$configuration\$`).FindSubmatch(sql)
	if len(match) != 2 {
		t.Fatal("missing config")
	}
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal(match[1], &b) != nil {
		t.Fatal("invalid config")
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		t.Fatal("configuration drift")
	}
	for _, s := range []string{"('feat_0044', 'automation.safe_upgrade')", "('safe_upgrades_monthly', 'MONTHLY')", "CREATE OR REPLACE FUNCTION guard_quota_reservation()", "SET sealed=true", "AND sealed", "INSERT INTO edition_limits", "BEGIN;", "COMMIT;"} {
		if !strings.Contains(string(sql), s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"UPDATE subscriptions", "UPDATE entitlements", "UPDATE quota_usage", "UPDATE entitlement_limits", "DELETE FROM"} {
		if strings.Contains(string(sql), s) {
			t.Fatal("customer mutation", s)
		}
	}
}
func TestPostgresSafeUpgradeMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, filepath.Base(f))
		if filepath.Base(f) == "000019_safe_upgrade.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "safe_upgrade.sql", "23505", "safe_upgrade_before.sql", "")
}
