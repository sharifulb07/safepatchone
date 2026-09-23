package migrationtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentGatewayMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000013_agent_gateway.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"REFERENCES repositories(organization_id,repository_id)", "REFERENCES organization_members(organization_id,user_id)", "PRIMARY KEY(organization_id,repository_id,lease_id,invocation_id)", "OLD.revoked AND NOT NEW.revoked", "NEW.uses<OLD.uses", "OLD.finished", "outcome='UNKNOWN'", "interval '600 seconds'", "agent_leases_no_truncate", "agent_invocations_no_truncate", "IS NOT DISTINCT FROM organization_id"} {
		if !strings.Contains(string(raw), required) {
			t.Fatal("missing invariant", required)
		}
	}
	for _, forbidden := range []string{"INSERT INTO entitlements", "UPDATE subscriptions", "UPDATE edition_features", "DELETE FROM"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatal("commercial history mutation", forbidden)
		}
	}
}
func TestPostgresAgentGatewayMigration(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(root(t), "migrations", "*.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, file := range files {
		names = append(names, filepath.Base(file))
		if filepath.Base(file) == "000013_agent_gateway.up.sql" {
			break
		}
	}
	testPostgresMigrations(t, names, "agent_gateway.sql", "42P07", "agent_gateway_before.sql", "")
}
