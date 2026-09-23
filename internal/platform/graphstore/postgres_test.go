package graphstore

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

func fixture(t *testing.T) (graph.Scope, evidence.Evidence, string) {
	t.Helper()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	s := graph.Scope{TenantID: "org", RepositoryIDs: []string{"repo_a", "repo_b"}, AsOf: now}
	n, err := evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: "a", TenantID: "org", ChangeID: "change", RunID: "run", Producer: "go", ProducerVersion: "1.0.0", Category: "verification", Type: "go.test", Status: evidence.StatusUnknown, Severity: evidence.SeverityInfo, Summary: "Unresolved", CreatedAt: now.Add(-time.Hour).Add(999 * time.Nanosecond)})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := n.CanonicalJSON()
	return s, n, string(raw)
}
func nodeSteps(s graph.Scope, n evidence.Evidence, raw string) []sqlStep {
	f, _ := n.Fields()
	return []sqlStep{{contains: "BEGIN"},
		{contains: "ON CONFLICT DO NOTHING", args: []driver.Value{s.TenantID, f.EvidenceID, "repo_a", f.ContentHash, raw, f.CreatedAt.Truncate(time.Microsecond)}},
		{contains: "WHERE tenant_id=$1 AND evidence_id=$2", args: []driver.Value{s.TenantID, f.EvidenceID}, rows: [][]driver.Value{{raw, "repo_a"}}}}
}
func readStep(s graph.Scope, raw string) sqlStep {
	return sqlStep{contains: "AND created_at >= $4 AND created_at <= $5", args: []driver.Value{s.TenantID, repositories(s), "a", s.NotBefore, s.AsOf}, rows: [][]driver.Value{{raw, "repo_a"}}}
}
func edgeFixture(s graph.Scope, r graph.Relationship) graph.Edge {
	return graph.Edge{TenantID: s.TenantID, EdgeID: "edge_1", FromEvidenceID: "a", ToEvidenceID: "b", Relationship: r, CreatedAt: s.AsOf}
}
func edgeSteps(s graph.Scope, e graph.Edge) []sqlStep {
	return []sqlStep{{contains: "BEGIN"},
		{contains: "AND created_at >= $5 AND created_at <= $6", args: []driver.Value{s.TenantID, repositories(s), e.FromEvidenceID, e.ToEvidenceID, s.NotBefore, s.AsOf}, rows: [][]driver.Value{{int64(2)}}},
		{contains: "ON CONFLICT DO NOTHING", args: []driver.Value{s.TenantID, e.EdgeID, e.FromEvidenceID, e.ToEvidenceID, string(e.Relationship), e.CreatedAt}},
		{contains: "WHERE tenant_id=$1 AND edge_id=$2", args: []driver.Value{s.TenantID, e.EdgeID}, rows: [][]driver.Value{{e.FromEvidenceID, e.ToEvidenceID, string(e.Relationship), e.CreatedAt}}}}
}

func TestEvidenceStoragePreservesCanonicalBytesAndRejectsReplacement(t *testing.T) {
	for _, mode := range []string{"insert", "retry", "replace_payload", "replace_repository", "begin_error", "insert_error", "read_error", "commit_error"} {
		t.Run(mode, func(t *testing.T) {
			s, n, raw := fixture(t)
			steps := nodeSteps(s, n, raw)
			failure := errors.New("database failure")
			want := error(nil)
			switch mode {
			case "replace_payload":
				steps[2].rows[0][0] = strings.Replace(raw, "Unresolved", "Changed", 1)
				want = graph.ErrConflict
			case "replace_repository":
				steps[2].rows[0][1] = "repo_b"
				want = graph.ErrConflict
			case "begin_error":
				steps = steps[:1]
				steps[0].err = failure
				want = failure
			case "insert_error":
				steps = steps[:2]
				steps[1].err = failure
				want = failure
			case "read_error":
				steps[2].err = failure
				want = failure
			case "commit_error":
				want = failure
			}
			store, conn := scriptStore(t, steps)
			if mode == "commit_error" {
				conn.commitError = failure
			}
			if err := store.PutEvidence(t.Context(), s, "repo_a", n); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if want == nil && (!conn.committed || conn.rolledBack) {
				t.Fatal("not committed")
			}
			if want != nil && mode != "begin_error" && mode != "commit_error" && !conn.rolledBack {
				t.Fatal("not rolled back")
			}
			after, _ := n.CanonicalJSON()
			if !bytes.Equal(after, []byte(raw)) {
				t.Fatal("node mutated")
			}
		})
	}
}

func TestGetEvidenceRejectsCorruptionAndForeignScopes(t *testing.T) {
	for _, mode := range []string{"success", "missing", "hash", "tenant", "repository", "identity", "expired", "error"} {
		t.Run(mode, func(t *testing.T) {
			s, n, raw := fixture(t)
			step := readStep(s, raw)
			want := error(graph.ErrGraphData)
			switch mode {
			case "success":
				want = nil
			case "missing":
				step.rows = nil
				want = graph.ErrNotFound
			case "hash":
				step.rows[0][0] = strings.Replace(raw, "Unresolved", "Corrupted", 1)
			case "repository":
				step.rows[0][1] = "foreign_repo"
			case "tenant", "identity", "expired":
				f, _ := n.Fields()
				f.ContentHash = ""
				if mode == "tenant" {
					f.TenantID = "other"
				}
				if mode == "identity" {
					f.EvidenceID = "other"
				}
				if mode == "expired" {
					f.CreatedAt = s.AsOf.Add(time.Hour)
				}
				changed, err := evidence.NewEvidence(f)
				if err != nil {
					t.Fatal(err)
				}
				b, _ := changed.CanonicalJSON()
				step.rows[0][0] = string(b)
			case "error":
				want = errors.New("database failure")
				step.err = want
			}
			store, _ := scriptStore(t, []sqlStep{step})
			result, err := store.GetEvidence(t.Context(), s, "a")
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if want == nil {
				b, _ := result.CanonicalJSON()
				if string(b) != raw {
					t.Fatal("canonical bytes changed")
				}
				f, _ := result.Fields()
				if f.Status != evidence.StatusUnknown {
					t.Fatal("UNKNOWN promoted")
				}
			}
		})
	}
}

func TestAllRelationshipsUseSameAtomicTenantScopedStorage(t *testing.T) {
	for _, r := range graph.Relationships() {
		t.Run(string(r), func(t *testing.T) {
			s, _, _ := fixture(t)
			e := edgeFixture(s, r)
			store, conn := scriptStore(t, edgeSteps(s, e))
			// Retrying sub-microsecond edge times uses the same stored identity.
			e.CreatedAt = e.CreatedAt.Add(999 * time.Nanosecond)
			if err := store.AddRelationship(t.Context(), s, e); err != nil || !conn.committed || conn.rolledBack {
				t.Fatal(err)
			}
		})
	}
}

func TestRelationshipErrorsRollback(t *testing.T) {
	for _, mode := range []string{"missing_endpoint", "both_missing", "conflict_id", "conflict_relation", "duplicate_relation", "begin_error", "lookup_error", "write_error", "read_error", "commit_error"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := fixture(t)
			e := edgeFixture(s, graph.Supports)
			steps := edgeSteps(s, e)
			failure := errors.New("database failure")
			want := error(graph.ErrConflict)
			switch mode {
			case "missing_endpoint", "both_missing":
				steps = steps[:2]
				count := int64(1)
				if mode == "both_missing" {
					count = 0
				}
				steps[1].rows = [][]driver.Value{{count}}
				want = graph.ErrNotFound
			case "conflict_id":
				steps[3].rows[0][0] = "different"
			case "conflict_relation":
				steps[3].rows[0][2] = "CONTRADICTS"
			case "duplicate_relation":
				steps[3].rows = nil
			case "begin_error":
				steps = steps[:1]
				steps[0].err = failure
				want = failure
			case "lookup_error":
				steps = steps[:2]
				steps[1].err = failure
				want = failure
			case "write_error":
				steps = steps[:3]
				steps[2].err = failure
				want = failure
			case "read_error":
				steps[3].err = failure
				want = failure
			case "commit_error":
				want = failure
			}
			store, conn := scriptStore(t, steps)
			if mode == "commit_error" {
				conn.commitError = failure
			}
			if err := store.AddRelationship(t.Context(), s, e); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if mode != "begin_error" && mode != "commit_error" && !conn.rolledBack {
				t.Fatal("missing rollback")
			}
		})
	}
}

func TestNeighborsScopesBothEndpointsAndPaginates(t *testing.T) {
	for _, mode := range []string{"page", "empty", "incoming", "outgoing", "relation", "foreign_endpoint", "expired_endpoint", "invalid_relation", "wrong_direction", "wrong_filter", "unordered", "too_many", "query_error", "rows_error", "scan_error"} {
		t.Run(mode, func(t *testing.T) {
			s, _, raw := fixture(t)
			q := graph.Query{EvidenceID: "a", Direction: graph.Both, Limit: 1}
			rows := [][]driver.Value{{"edge_1", "a", "b", "SUPPORTS", s.AsOf, "repo_a", "repo_b", s.AsOf, s.AsOf}, {"edge_2", "b", "a", "CONTRADICTS", s.AsOf, "repo_b", "repo_a", s.AsOf, s.AsOf}}
			bad := false
			switch mode {
			case "empty":
				rows = nil
			case "incoming":
				q.Direction = graph.Incoming
				rows = rows[1:]
			case "outgoing":
				q.Direction = graph.Outgoing
				rows = rows[:1]
			case "relation":
				q.Relationship = graph.Supports
				rows = rows[:1]
			case "foreign_endpoint":
				rows[0][6] = "secret_repo"
				bad = true
			case "expired_endpoint":
				rows[0][8] = s.AsOf.Add(time.Hour)
				bad = true
			case "invalid_relation":
				rows[0][3] = "PASS"
				bad = true
			case "wrong_direction":
				q.Direction = graph.Incoming
				bad = true
			case "wrong_filter":
				q.Relationship = graph.Invalidates
				bad = true
			case "unordered":
				q.AfterEdgeID = "edge_1"
				bad = true
			case "too_many":
				rows = append(rows, []driver.Value{"edge_3", "b", "a", "DERIVED_FROM", s.AsOf, "repo_b", "repo_a", s.AsOf, s.AsOf})
				bad = true
			case "scan_error":
				rows[0] = []driver.Value{"edge_1"}
				bad = true
			case "query_error", "rows_error":
				bad = true
			}
			step := sqlStep{contains: "AND t.repository_id IN (SELECT jsonb_array_elements_text($2::jsonb))", args: []driver.Value{s.TenantID, repositories(s), s.NotBefore, s.AsOf, q.EvidenceID, string(q.Direction), string(q.Relationship), q.AfterEdgeID, int64(q.Limit + 1)}, rows: rows}
			failure := errors.New("database read failure")
			if mode == "query_error" {
				step.err = failure
			}
			if mode == "rows_error" {
				step.nextError = failure
			}
			store, _ := scriptStore(t, []sqlStep{readStep(s, raw), step})
			page, err := store.Neighbors(t.Context(), s, q)
			if (err != nil) != bad {
				t.Fatal(page, err)
			}
			if bad {
				if len(page.Edges) != 0 {
					t.Fatal("partial evidence returned on error")
				}
				return
			}
			if mode == "page" && (len(page.Edges) != 1 || page.NextCursor != "edge_1") {
				t.Fatal(page)
			}
			if mode == "empty" && (page.Edges == nil || len(page.Edges) != 0 || page.NextCursor != "") {
				t.Fatal(page)
			}
			if mode != "page" && page.NextCursor != "" {
				t.Fatal("spurious continuation")
			}
		})
	}
}

func TestInvalidInputAndCancellationDoNotTouchDatabase(t *testing.T) {
	s, n, _ := fixture(t)
	store, _ := scriptStore(t, nil)
	if _, err := NewPostgres(nil); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	if err := store.PutEvidence(t.Context(), s, "foreign", n); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	foreign := s
	foreign.TenantID = "other"
	if err := store.PutEvidence(t.Context(), foreign, "repo_a", n); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	e := edgeFixture(s, graph.Supports)
	e.TenantID = "other"
	if err := store.AddRelationship(t.Context(), s, e); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	if _, err := store.GetEvidence(t.Context(), s, "' OR true--"); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	if _, err := store.Neighbors(t.Context(), s, graph.Query{EvidenceID: "a", Direction: graph.Both, Limit: graph.MaxPageSize + 1}); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.PutEvidence(ctx, s, "repo_a", n); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.GetEvidence(ctx, s, "a"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := store.AddRelationship(ctx, s, edgeFixture(s, graph.Supports)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.Neighbors(ctx, s, graph.Query{EvidenceID: "a", Direction: graph.Both, Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
