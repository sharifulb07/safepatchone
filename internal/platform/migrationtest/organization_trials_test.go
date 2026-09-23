package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrganizationTrialMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000010_organization_trials.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"organization_id text PRIMARY KEY REFERENCES organizations", "CREATE TABLE organization_trial_grants", "REFERENCES organization_trials(organization_id,trial_id)", "REFERENCES entitlements(organization_id,entitlement_id)", "organization_trials_immutable", "organization_trial_grants_immutable", "1209600", "validate_organization_trial_grant", "e.source='TRIAL'"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing invariant", required)
		}
	}
	for _, forbidden := range []string{"CREATE TABLE entitlements", "UPDATE subscriptions", "DELETE FROM", "UPDATE edition_features", "UPDATE quota_usage"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("trial migration changes existing data", forbidden)
		}
	}
}
func TestPostgresOrganizationTrialsMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql", "000008_organization_entities.up.sql", "000009_subscription_sync.up.sql", "000010_organization_trials.up.sql"}, "organization_trials.sql", "42P07", "organization_trials_before.sql", "")
}
