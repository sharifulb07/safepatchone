package graph

import (
	"encoding/json"
	"slices"
	"strings"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
)

const EvidenceManifestSchema = "safepatch.evidencegraph.manifest.v1"
const MaxManifestNodes = 4096
const MaxManifestEdges = 8192

type EvidencePin struct {
	EvidenceID string `json:"evidence_id"`
	Digest     string `json:"digest"`
}

type EvidenceManifestFields struct {
	Schema   string        `json:"schema"`
	Change   domain.Change `json:"change"`
	ChangeID string        `json:"change_id"`
	RunID    string        `json:"run_id"`
	Nodes    []EvidencePin `json:"nodes"`
	Edges    []Edge        `json:"edges"`
}

// EvidenceManifest is an explicitly supplied run-scoped subgraph, never a
// completeness claim about the whole organization's EvidenceGraph. It pins
// evidence assertions, not ChangeGraph source entities or an unbounded query.
type EvidenceManifest struct{ canonical string }

func NewEvidenceManifest(change domain.Change, run string, nodes []evidence.Evidence, edges []Edge) (EvidenceManifest, error) {
	if change.Validate() != nil || !evidence.ValidIdentifier(run) || len(nodes) > MaxManifestNodes || len(edges) > MaxManifestEdges {
		return EvidenceManifest{}, ErrInvalidGraph
	}
	f := EvidenceManifestFields{Schema: EvidenceManifestSchema, Change: change, ChangeID: change.ID(), RunID: run, Nodes: []EvidencePin{}, Edges: []Edge{}}
	seen := map[string]bool{}
	for _, node := range nodes {
		n, err := node.Fields()
		if err != nil || n.TenantID != change.OrganizationID || n.ChangeID != change.ID() || n.RunID != run || seen[n.EvidenceID] {
			return EvidenceManifest{}, ErrInvalidGraph
		}
		seen[n.EvidenceID] = true
		f.Nodes = append(f.Nodes, EvidencePin{EvidenceID: n.EvidenceID, Digest: n.ContentHash})
	}
	seenEdges := map[string]bool{}
	for _, edge := range edges {
		e, err := edge.Canonical()
		if err != nil || e.TenantID != change.OrganizationID || seenEdges[e.EdgeID] || !seen[e.FromEvidenceID] || !seen[e.ToEvidenceID] {
			return EvidenceManifest{}, ErrInvalidGraph
		}
		seenEdges[e.EdgeID] = true
		f.Edges = append(f.Edges, e)
	}
	slices.SortFunc(f.Nodes, func(a, b EvidencePin) int { return strings.Compare(a.EvidenceID, b.EvidenceID) })
	slices.SortFunc(f.Edges, func(a, b Edge) int { return strings.Compare(a.EdgeID, b.EdgeID) })
	raw, err := json.Marshal(f)
	if err != nil {
		return EvidenceManifest{}, ErrInvalidGraph
	}
	return EvidenceManifest{canonical: string(raw)}, nil
}

func (m EvidenceManifest) MarshalJSON() ([]byte, error) {
	if m.canonical == "" {
		return nil, ErrInvalidGraph
	}
	return []byte(m.canonical), nil
}
func (*EvidenceManifest) UnmarshalJSON([]byte) error { return ErrInvalidGraph }
func (m EvidenceManifest) Digest() string {
	if m.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(EvidenceManifestSchema + "\n" + m.canonical))
}
