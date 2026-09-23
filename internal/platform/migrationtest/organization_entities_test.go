package migrationtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOrganizationEntityMigrationContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "migrations", "000008_organization_entities.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	for _, required := range []string{"CREATE TABLE users", "CREATE TABLE organizations", "CREATE TABLE organization_members", "CREATE TABLE projects", "CREATE TABLE repositories", "CREATE TABLE subscriptions", "CREATE TABLE entitlements", "PRIMARY KEY (organization_id,user_id)", "PRIMARY KEY (organization_id,repository_id)", "FOREIGN KEY (organization_id,project_id) REFERENCES projects(organization_id,project_id)", "FOREIGN KEY (organization_id,subscription_id) REFERENCES subscriptions(organization_id,subscription_id)", "subscriptions_one_current_per_organization", "quota_accounts_organization_fk", "'PENDING',CURRENT_TIMESTAMP", "UNION SELECT tenant_id FROM evidence_graph_nodes", "entitlements_immutable", "entity ownership is immutable"} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing organization invariant", required)
		}
	}
	subscription := regexp.MustCompile(`(?s)CREATE TABLE subscriptions \((.*?)\n\);`).FindStringSubmatch(sql)
	if len(subscription) != 2 {
		t.Fatal("subscription table missing")
	}
	for _, forbidden := range []string{"repository_id", "user_id", "product_version"} {
		if strings.Contains(subscription[1], forbidden) {
			t.Fatal("incorrect subscription owner", forbidden)
		}
	}
	if strings.Contains(sql, "CREATE TABLE usage") || strings.Contains(sql, "CREATE TABLE quota_usage") {
		t.Fatal("duplicate usage ledger")
	}
	for _, fixture := range []string{"organization_entities_before.sql", "organization_entities.sql"} {
		if _, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", fixture)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestPostgresOrganizationEntitiesMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql", "000008_organization_entities.up.sql"}, "organization_entities.sql", "42P07", "organization_entities_before.sql", "")
}
