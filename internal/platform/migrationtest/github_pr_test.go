package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubPRMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000012_github_pull_requests.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, invariant := range []string{"REFERENCES onboarding_github_repositories(organization_id,repository_id)", "REFERENCES organization_members(organization_id,user_id)", "github_pr_deliveries_immutable", "github_pr_jobs_no_truncate", "NEW.canonical_result IS DISTINCT FROM OLD.canonical_result", "IS NOT DISTINCT FROM organization_id", "IS NOT DISTINCT FROM repository_id"} {
		if !strings.Contains(string(raw), invariant) {
			t.Fatal("missing invariant", invariant)
		}
	}
	for _, forbidden := range []string{"UPDATE subscriptions", "INSERT INTO entitlements", "UPDATE edition_features", "DELETE FROM", "UPDATE quota_usage"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("migration changes commercial state", forbidden)
		}
	}
}
func TestPostgresGitHubPRMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql", "000008_organization_entities.up.sql", "000009_subscription_sync.up.sql", "000010_organization_trials.up.sql", "000011_onboarding.up.sql", "000012_github_pull_requests.up.sql"}, "github_pr.sql", "42P07", "github_pr_before.sql", "")
}
