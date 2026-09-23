package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboardingMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000011_onboarding.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, invariant := range []string{
		"CREATE TABLE login_identities", "CREATE TABLE login_sessions", "CREATE TABLE login_attempts",
		"CREATE TABLE onboarding_states", "UNIQUE(creator_user_id,creation_attempt)",
		"REFERENCES repositories(organization_id,repository_id)",
		"REFERENCES onboarding_github_connections(organization_id,installation_id)",
		"UNIQUE(organization_id,onboarding_revision)", "onboarding_state_no_truncate",
		"onboarding_proof_no_truncate", "IS NOT DISTINCT FROM organization_id",
		"length(evidence_reference) BETWEEN 1 AND 256",
	} {
		if !strings.Contains(string(raw), invariant) {
			t.Fatal("missing invariant", invariant)
		}
	}
	for _, forbidden := range []string{"UPDATE subscriptions", "INSERT INTO entitlements", "DELETE FROM", "UPDATE edition_features", "UPDATE quota_usage"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("onboarding migration changes existing commercial data", forbidden)
		}
	}
}

func TestPostgresOnboardingMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql", "000008_organization_entities.up.sql", "000009_subscription_sync.up.sql", "000010_organization_trials.up.sql", "000011_onboarding.up.sql"}, "onboarding.sql", "42P07", "onboarding_before.sql", "")
}
