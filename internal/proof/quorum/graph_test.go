package quorum_test

import (
	"context"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"testing"
	"time"
)

type graphStore struct {
	nodes  map[string]evidence.Evidence
	edges  map[string]graph.Edge
	writes int
	fail   bool
}

func (g *graphStore) GetEvidence(_ context.Context, s graph.Scope, id string) (evidence.Evidence, error) {
	n, ok := g.nodes[id]
	if !ok {
		return evidence.Evidence{}, graph.ErrNotFound
	}
	f, _ := n.Fields()
	if f.TenantID != s.TenantID || !s.Contains("repo", f.CreatedAt) {
		return evidence.Evidence{}, graph.ErrNotFound
	}
	return n, nil
}
func (g *graphStore) PutEvidence(_ context.Context, _ graph.Scope, _ string, n evidence.Evidence) error {
	g.writes++
	f, _ := n.Fields()
	if old, ok := g.nodes[f.EvidenceID]; ok {
		o, _ := old.Fields()
		if o.ContentHash != f.ContentHash {
			return graph.ErrConflict
		}
	}
	g.nodes[f.EvidenceID] = n
	return nil
}
func (g *graphStore) AddRelationship(_ context.Context, _ graph.Scope, e graph.Edge) error {
	if g.fail {
		return graph.ErrGraphData
	}
	g.edges[e.EdgeID] = e
	return nil
}
func (g *graphStore) Neighbors(context.Context, graph.Scope, graph.Query) (graph.Page, error) {
	return graph.Page{}, graph.ErrInvalidGraph
}

func TestQuorumGraphPersistenceAndRetry(t *testing.T) {
	f, in := fixture(t)
	r := evaluate(t, f, in)
	store := &graphStore{nodes: map[string]evidence.Evidence{}, edges: map[string]graph.Edge{}}
	for _, n := range in.Evidence {
		fields, _ := n.Fields()
		store.nodes[fields.EvidenceID] = n
	}
	scope := graph.Scope{TenantID: "org", RepositoryIDs: []string{"repo"}, NotBefore: in.AsOf.Add(-time.Hour), AsOf: in.AsOf}
	intent := evidence.Digest([]byte("intent"))
	store.fail = true
	if _, err := r.Store(t.Context(), store, scope, "quorum.result", intent); err == nil {
		t.Fatal("partial graph write reported success")
	}
	store.fail = false
	for range 2 {
		if _, err := r.Store(t.Context(), store, scope, "quorum.result", intent); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.nodes) != 5 || len(store.edges) != 4 {
		t.Fatal("retry duplicated graph", store)
	}
	writes := store.writes
	scope.TenantID = "foreign"
	if _, err := r.Store(t.Context(), store, scope, "other", intent); err == nil {
		t.Fatal("foreign scope accepted")
	}
	scope.TenantID = "org"
	delete(store.nodes, "replay")
	if _, err := r.Store(t.Context(), store, scope, "other", intent); err == nil || store.writes != writes {
		t.Fatal("unretained evidence stored")
	}
}
