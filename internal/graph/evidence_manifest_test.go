package graph_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

func TestEvidenceManifestBindsRunNodesAndRelationships(t *testing.T) {
	c := domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	nodes := []evidence.Evidence{}
	for _, id := range []string{"a", "b"} {
		n, err := evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: id, TenantID: "org", ChangeID: c.ID(), RunID: "run", Producer: "verifier", ProducerVersion: "1.0.0", Category: "verification", Type: "tests", Status: evidence.StatusPass, Severity: evidence.SeverityInfo, Confidence: 1, Summary: "tests", Payload: json.RawMessage(`{}`), CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	edge := graph.Edge{TenantID: "org", EdgeID: "e", FromEvidenceID: "a", ToEvidenceID: "b", Relationship: graph.Supports, CreatedAt: now}
	m, err := graph.NewEvidenceManifest(c, "run", nodes, []graph.Edge{edge})
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := graph.NewEvidenceManifest(c, "run", []evidence.Evidence{nodes[1], nodes[0]}, []graph.Edge{edge})
	if err != nil || m.Digest() != reordered.Digest() {
		t.Fatal("order affected digest", err)
	}
	edge.Relationship = graph.Contradicts
	changed, _ := graph.NewEvidenceManifest(c, "run", nodes, []graph.Edge{edge})
	if changed.Digest() == m.Digest() {
		t.Fatal("edge assertion omitted")
	}
	for _, mode := range []string{"foreign_org", "foreign_run", "duplicate_node", "duplicate_edge", "dangling", "foreign_edge", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			change, run, ns, es := c, "run", nodes, []graph.Edge{edge}
			switch mode {
			case "foreign_org":
				change.OrganizationID = "other"
			case "foreign_run":
				run = "other"
			case "duplicate_node":
				ns = append([]evidence.Evidence{nodes[0]}, nodes...)
			case "duplicate_edge":
				es = append(es, edge)
			case "dangling":
				es[0].ToEvidenceID = "missing"
			case "foreign_edge":
				es[0].TenantID = "other"
			case "too_many":
				ns = make([]evidence.Evidence, graph.MaxManifestNodes+1)
			}
			if _, err := graph.NewEvidenceManifest(change, run, ns, es); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
	empty, err := graph.NewEvidenceManifest(c, "run", nil, nil)
	if err != nil || empty.Digest() == m.Digest() {
		t.Fatal("empty graph must remain representable for UNKNOWN runs")
	}
}
