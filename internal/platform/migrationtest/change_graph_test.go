package migrationtest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"safepatch/internal/graph"
)

func TestCausalChangeGraphMigrationContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root(t), "migrations", "000007_causal_change_graph.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for column, want := range map[string][]string{"kind": entityStrings(), "relation": relationStrings()} {
		m := regexp.MustCompile(column + ` IN \(([^)]+)\)`).FindStringSubmatch(sql)
		if len(m) != 2 {
			t.Fatal("missing constraint", column)
		}
		var got []string
		for _, v := range strings.Split(m[1], ",") {
			got = append(got, strings.Trim(v, " '"))
		}
		if !slices.Equal(got, want) {
			t.Fatal("SQL/domain enum drift", got, want)
		}
	}
	for _, required := range []string{
		"PRIMARY KEY (tenant_id,repository_id,revision,entity_id)", "PRIMARY KEY (tenant_id,link_id)",
		"FOREIGN KEY (tenant_id,from_repository,from_revision,from_entity) REFERENCES causal_change_nodes(tenant_id,repository_id,revision,entity_id)",
		"FOREIGN KEY (tenant_id,to_repository,to_revision,to_entity) REFERENCES causal_change_nodes(tenant_id,repository_id,revision,entity_id)",
		"FOREIGN KEY (tenant_id,evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id)",
		"BEFORE UPDATE OR DELETE OR TRUNCATE ON causal_change_nodes", "BEFORE UPDATE OR DELETE OR TRUNCATE ON causal_change_links",
		"CREATE INDEX causal_change_links_outgoing", "CREATE INDEX causal_change_links_incoming",
		"CHECK (from_repository <> to_repository OR from_revision = to_revision)",
		"IS NOT DISTINCT FROM tenant_id", "IS NOT DISTINCT FROM from_revision", "IS NOT DISTINCT FROM to_revision",
	} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing persistence invariant", required)
		}
	}
	for _, name := range []string{"change_graph_before.sql", "change_graph.sql"} {
		if _, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", name)); err != nil {
			t.Fatal(err)
		}
	}
}
func entityStrings() []string {
	var out []string
	for _, v := range graph.EntityKinds() {
		out = append(out, string(v))
	}
	return out
}
func relationStrings() []string {
	var out []string
	for _, v := range graph.CausalRelations() {
		out = append(out, string(v))
	}
	return out
}

func TestPostgresCausalChangeGraphMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql", "000007_causal_change_graph.up.sql"}, "change_graph.sql", "42P07", "change_graph_before.sql", "")
}
