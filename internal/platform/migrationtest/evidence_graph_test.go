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

func TestEvidenceGraphMigrationContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root(t), "migrations", "000005_evidence_graph.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	match := regexp.MustCompile(`relationship IN \(([^)]+)\)`).FindStringSubmatch(sql)
	if len(match) != 2 {
		t.Fatal("missing relationship constraint")
	}
	var got []graph.Relationship
	for _, item := range strings.Split(match[1], ",") {
		got = append(got, graph.Relationship(strings.Trim(item, " '")))
	}
	if !slices.Equal(got, graph.Relationships()) {
		t.Fatal("SQL/domain relationships disagree", got)
	}
	for _, required := range []string{
		"PRIMARY KEY (tenant_id,evidence_id)", "PRIMARY KEY (tenant_id,edge_id)",
		"UNIQUE (tenant_id,from_evidence_id,to_evidence_id,relationship)",
		"FOREIGN KEY (tenant_id,from_evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id)",
		"FOREIGN KEY (tenant_id,to_evidence_id) REFERENCES evidence_graph_nodes(tenant_id,evidence_id)",
		"BEFORE UPDATE OR DELETE OR TRUNCATE ON evidence_graph_nodes", "BEFORE UPDATE OR DELETE OR TRUNCATE ON evidence_graph_edges",
		"CREATE INDEX evidence_graph_edges_outgoing", "CREATE INDEX evidence_graph_edges_incoming",
	} {
		if !strings.Contains(sql, required) {
			t.Fatal("missing shared persistence boundary", required)
		}
	}
	for _, fixture := range []string{"evidence_graph_before.sql", "evidence_graph.sql"} {
		if _, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", fixture)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresEvidenceGraphMigration(t *testing.T) {
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql"}, "evidence_graph.sql", "42P07", "evidence_graph_before.sql", "")
}
