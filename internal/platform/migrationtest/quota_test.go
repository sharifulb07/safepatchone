package migrationtest

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	domain "safepatch/internal/domain/entitlement"
)

func TestQuotaMigrationKeyContract(t *testing.T) {
	var data []byte
	for _, name := range []string{"000003_entitlement_limits_quotas.up.sql", "000014_replay.up.sql", "000019_safe_upgrade.up.sql"} {
		raw, err := os.ReadFile(filepath.Join(root(t), "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, raw...)
	}
	rows := regexp.MustCompile(`\('([a-z_]+)', '(CAPACITY|MONTHLY|CEILING)'\)`).FindAllStringSubmatch(string(data), -1)
	if len(rows) != len(domain.LimitKeys()) {
		t.Fatal("quota key count differs", rows)
	}
	seen := map[domain.LimitKey]bool{}
	for _, row := range rows {
		key := domain.LimitKey(row[1])
		kind, err := key.Accounting()
		if err != nil || string(kind) != row[2] || seen[key] {
			t.Fatal("quota key contract differs", row, err)
		}
		seen[key] = true
	}
}

func TestPostgresQuotaMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql"}, "quota.sql", "42P07", "", "")
}
