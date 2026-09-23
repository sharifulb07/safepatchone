package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionSyncMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000009_subscription_sync.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"CREATE TABLE billing_connections", "CREATE TABLE billing_customers", "CREATE TABLE billing_events", "CREATE TABLE billing_subscription_state", "CREATE TABLE billing_subscription_history", "PRIMARY KEY(connection_id,event_id)", "UNIQUE(connection_id,customer_reference)", "REFERENCES subscriptions(organization_id,subscription_id)", "REFERENCES billing_events(connection_id,event_id,organization_id)", "billing_history_immutable", "OLD.applied_at IS NOT NULL", "NEW.revision<>OLD.revision+1"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing billing invariant", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE subscriptions", "CREATE TABLE entitlements", "DELETE FROM quota_usage", "UPDATE edition_features", "stripe_entitlements"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("forked or destructive schema", forbidden)
		}
	}
}
func TestPostgresSubscriptionSyncMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql", "000008_organization_entities.up.sql", "000009_subscription_sync.up.sql"}, "subscription_sync.sql", "42P07", "subscription_sync_before.sql", "")
}
