package graphstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

func changeFixture(t *testing.T) (graph.RevisionScope, graph.ChangeNode, graph.ChangeLink, string) {
	t.Helper()
	access, _, raw := fixture(t)
	scope := graph.RevisionScope{Access: access, Revisions: []graph.RevisionPin{{RepositoryID: "repo_a", Revision: "rev1"}, {RepositoryID: "repo_b", Revision: "rev2"}}}
	node, err := graph.NewChangeNode(graph.ChangeNodeFields{Schema: graph.ChangeNodeSchema, TenantID: "org", Ref: graph.EntityRef{RepositoryID: "repo_a", Revision: "rev1", EntityID: "symbol"}, Kind: graph.SymbolEntity, Name: "handler", InputHash: evidence.Digest([]byte("source")), CreatedAt: access.AsOf.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	f, _ := node.Fields()
	link, err := graph.NewChangeLink(graph.ChangeLinkFields{Schema: graph.ChangeLinkSchema, TenantID: "org", LinkID: "link1", From: f.Ref, To: graph.EntityRef{RepositoryID: "repo_b", Revision: "rev2", EntityID: "service"}, Relation: graph.RuntimeCalls, Basis: graph.BasisUnknown, EvidenceID: "a", CreatedAt: access.AsOf})
	if err != nil {
		t.Fatal(err)
	}
	return scope, node, link, raw
}
func changeReadStep(s graph.RevisionScope, n graph.ChangeNode) sqlStep {
	f, _ := n.Fields()
	raw, _ := n.MarshalJSON()
	return sqlStep{contains: "AND created_at BETWEEN $5 AND $6", args: []driver.Value{f.TenantID, f.Ref.RepositoryID, f.Ref.Revision, f.Ref.EntityID, s.Access.NotBefore, s.Access.AsOf}, rows: [][]driver.Value{{string(raw), n.ContentHash(), string(f.Kind), f.CreatedAt}}}
}
func changeWriteSteps(n graph.ChangeNode) []sqlStep {
	f, _ := n.Fields()
	raw, _ := n.MarshalJSON()
	return []sqlStep{{contains: "BEGIN"}, {contains: "ON CONFLICT DO NOTHING", args: []driver.Value{f.TenantID, f.Ref.RepositoryID, f.Ref.Revision, f.Ref.EntityID, string(f.Kind), n.ContentHash(), string(raw), f.CreatedAt}}, {contains: "WHERE tenant_id=$1 AND repository_id=$2 AND revision=$3 AND entity_id=$4", args: []driver.Value{f.TenantID, f.Ref.RepositoryID, f.Ref.Revision, f.Ref.EntityID}, rows: [][]driver.Value{{string(raw)}}}}
}
func changeLinkSteps(s graph.RevisionScope, l graph.ChangeLink, raw string) []sqlStep {
	f, _ := l.Fields()
	canonical, _ := l.MarshalJSON()
	return []sqlStep{readStep(s.Access, raw), {contains: "BEGIN"}, {contains: "AND created_at BETWEEN $8 AND $9", args: []driver.Value{f.TenantID, f.From.RepositoryID, f.From.Revision, f.From.EntityID, f.To.RepositoryID, f.To.Revision, f.To.EntityID, s.Access.NotBefore, f.CreatedAt}, rows: [][]driver.Value{{int64(2)}}}, {contains: "ON CONFLICT DO NOTHING", args: []driver.Value{f.TenantID, f.LinkID, f.From.RepositoryID, f.From.Revision, f.From.EntityID, f.To.RepositoryID, f.To.Revision, f.To.EntityID, string(f.Relation), f.EvidenceID, l.ContentHash(), string(canonical), f.CreatedAt}}, {contains: "SELECT canonical_link", args: []driver.Value{f.TenantID, f.LinkID}, rows: [][]driver.Value{{string(canonical)}}}}
}

func TestChangeWritesImmutableRetryAndErrors(t *testing.T) {
	for _, kind := range []string{"node", "link"} {
		for _, mode := range []string{"insert", "retry", "conflict", "begin", "write", "read", "commit"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				scope, node, link, raw := changeFixture(t)
				steps := changeWriteSteps(node)
				offset := 0
				if kind == "link" {
					steps = changeLinkSteps(scope, link, raw)
					offset = 1
				}
				failure := errors.New("database error")
				var want error
				switch mode {
				case "conflict":
					steps[len(steps)-1].rows[0][0] = "different"
					want = graph.ErrConflict
				case "begin":
					steps = steps[:offset+1]
					steps[offset].err = failure
					want = failure
				case "write":
					steps = steps[:len(steps)-1]
					steps[len(steps)-1].err = failure
					want = failure
				case "read":
					steps[len(steps)-1].err = failure
					want = failure
				case "commit":
					want = failure
				}
				if mode == "retry" {
					original := append([]sqlStep(nil), steps...)
					steps = append(steps, original...)
				}
				store, conn := scriptStore(t, steps)
				if mode == "commit" {
					conn.commitError = failure
				}
				put := func() error {
					if kind == "node" {
						return store.PutChangeNode(t.Context(), scope, node)
					}
					return store.PutChangeLink(t.Context(), scope, link)
				}
				if err := put(); !errors.Is(err, want) {
					t.Fatal(err, want)
				}
				if mode == "retry" {
					if err := put(); err != nil {
						t.Fatal(err)
					}
				}
				if want == nil && !conn.committed {
					t.Fatal("not committed")
				}
				if want != nil && mode != "begin" && mode != "commit" && !conn.rolledBack {
					t.Fatal("not rolled back")
				}
			})
		}
	}
}

func TestChangeNodeReadChecksRevisionTenantHashAndProjection(t *testing.T) {
	for _, mode := range []string{"valid", "new_revision", "absent", "hash", "tenant", "revision", "kind", "time", "decode", "error"} {
		t.Run(mode, func(t *testing.T) {
			s, n, _, _ := changeFixture(t)
			f, _ := n.Fields()
			if mode == "new_revision" {
				s.Revisions[0].Revision = "rev3"
				f.Ref.Revision = "rev3"
				n, _ = graph.NewChangeNode(f)
			}
			step := changeReadStep(s, n)
			var want error
			switch mode {
			case "absent":
				step.rows = nil
				want = graph.ErrNotFound
			case "hash":
				step.rows[0][1] = "wrong"
				want = graph.ErrGraphData
			case "kind":
				step.rows[0][2] = "file"
				want = graph.ErrGraphData
			case "time":
				step.rows[0][3] = s.Access.AsOf
				want = graph.ErrGraphData
			case "decode":
				step.rows[0][0] = "{}"
				want = graph.ErrGraphData
			case "tenant", "revision":
				bad := f
				if mode == "tenant" {
					bad.TenantID = "foreign"
				} else {
					bad.Ref.Revision = "foreign"
				}
				poison, _ := graph.NewChangeNode(bad)
				raw, _ := poison.MarshalJSON()
				step.rows[0][0] = string(raw)
				step.rows[0][1] = poison.ContentHash()
				want = graph.ErrGraphData
			case "error":
				want = errors.New("database error")
				step.err = want
			}
			store, _ := scriptStore(t, []sqlStep{step})
			got, err := store.GetChangeNode(t.Context(), s, f.Ref)
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if err == nil && got.ContentHash() != n.ContentHash() {
				t.Fatal("changed immutable node")
			}
		})
	}
}

func TestChangeLinkRequiresVisibleEvidenceAndEarlierEndpoints(t *testing.T) {
	for _, mode := range []string{"no_evidence", "no_endpoint", "endpoint_error", "future_evidence"} {
		t.Run(mode, func(t *testing.T) {
			s, _, l, raw := changeFixture(t)
			steps := changeLinkSteps(s, l, raw)
			want := error(graph.ErrNotFound)
			switch mode {
			case "no_evidence":
				steps = steps[:1]
				steps[0].rows = nil
			case "no_endpoint":
				steps = steps[:3]
				steps[2].rows[0][0] = int64(1)
			case "endpoint_error":
				steps = steps[:3]
				want = errors.New("count failed")
				steps[2].err = want
			case "future_evidence":
				f, _ := l.Fields()
				f.CreatedAt = s.Access.AsOf.Add(-2 * time.Hour)
				l, _ = graph.NewChangeLink(f)
				steps = steps[:1]
				want = graph.ErrInvalidGraph
			}
			store, _ := scriptStore(t, steps)
			if err := store.PutChangeLink(t.Context(), s, l); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
		})
	}
}

func changeLinkRow(l graph.ChangeLink) []driver.Value {
	f, _ := l.Fields()
	raw, _ := l.MarshalJSON()
	earlier := f.CreatedAt.Add(-time.Hour)
	return []driver.Value{string(raw), l.ContentHash(), f.LinkID, f.From.RepositoryID, f.From.Revision, f.From.EntityID, f.To.RepositoryID, f.To.Revision, f.To.EntityID, string(f.Relation), f.EvidenceID, f.CreatedAt, earlier, earlier, "repo_a", earlier}
}
func TestChangeNeighborsScopedPaginationAndPoisoning(t *testing.T) {
	for _, mode := range []string{"valid", "incoming", "both", "empty", "page", "hash", "revision", "tenant", "direction", "relation", "source_repo", "source_time", "node_time", "index", "order", "query_error", "late_error", "scan_error"} {
		t.Run(mode, func(t *testing.T) {
			s, n, l, _ := changeFixture(t)
			f, _ := n.Fields()
			lf, _ := l.Fields()
			q := graph.ChangeQuery{Root: f.Ref, Direction: graph.Outgoing, Relation: graph.RuntimeCalls, Limit: 1}
			if mode == "incoming" {
				q.Direction = graph.Incoming
				lf.From, lf.To = lf.To, lf.From
				l, _ = graph.NewChangeLink(lf)
			}
			if mode == "both" {
				q.Direction = graph.Both
			}
			row := changeLinkRow(l)
			rows := [][]driver.Value{row}
			var want error
			switch mode {
			case "empty":
				rows = nil
			case "page":
				lf.LinkID = "link2"
				next, _ := graph.NewChangeLink(lf)
				rows = append(rows, changeLinkRow(next))
			case "hash":
				row[1] = "poison"
			case "revision":
				lf.To.Revision = "wrong"
				poison, _ := graph.NewChangeLink(lf)
				rows[0] = changeLinkRow(poison)
			case "tenant":
				lf.TenantID = "foreign"
				poison, _ := graph.NewChangeLink(lf)
				rows[0] = changeLinkRow(poison)
			case "direction":
				lf.From, lf.To = lf.To, lf.From
				poison, _ := graph.NewChangeLink(lf)
				rows[0] = changeLinkRow(poison)
			case "relation":
				lf.Relation = graph.Causes
				poison, _ := graph.NewChangeLink(lf)
				rows[0] = changeLinkRow(poison)
			case "source_repo":
				row[14] = "secret_repo"
			case "source_time":
				row[15] = s.Access.AsOf.Add(time.Hour)
			case "node_time":
				row[12] = s.Access.AsOf.Add(time.Hour)
			case "index":
				row[8] = "wrong"
			case "order":
				rows = append(rows, changeLinkRow(l))
			case "scan_error":
				rows[0] = row[:2]
			}
			switch mode {
			case "hash", "revision", "tenant", "direction", "relation", "source_repo", "source_time", "node_time", "index", "order":
				want = graph.ErrGraphData
			}
			step := sqlStep{contains: changeNeighborsSQL, args: []driver.Value{s.Access.TenantID, pins(s), repositories(s.Access), s.Access.NotBefore, s.Access.AsOf, q.Root.RepositoryID, q.Root.Revision, q.Root.EntityID, string(q.Direction), string(q.Relation), q.AfterLinkID, int64(q.Limit + 1)}, rows: rows}
			if mode == "query_error" {
				want = errors.New("query error")
				step.err = want
			}
			if mode == "late_error" {
				want = errors.New("rows error")
				step.nextError = want
			}
			store, _ := scriptStore(t, []sqlStep{changeReadStep(s, n), step})
			page, err := store.ChangeNeighbors(t.Context(), s, q)
			if mode == "scan_error" {
				if err == nil {
					t.Fatal("scan error ignored")
				}
				return
			}
			if !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if err != nil {
				if len(page.Links) != 0 || page.NextCursor != "" {
					t.Fatal("partial success leaked")
				}
				return
			}
			if mode == "page" && page.NextCursor != "link1" {
				t.Fatal(page)
			}
			if mode != "empty" && len(page.Links) != 1 {
				t.Fatal(page)
			}
		})
	}
}

func TestChangeScopeDenialsDoNotReachSQL(t *testing.T) {
	s, n, l, _ := changeFixture(t)
	f, _ := n.Fields()
	store, _ := scriptStore(t, nil)
	bad := s
	bad.Revisions = []graph.RevisionPin{{RepositoryID: "repo_a", Revision: "rev9"}, {RepositoryID: "repo_b", Revision: "rev2"}}
	if _, err := store.GetChangeNode(t.Context(), bad, f.Ref); !errors.Is(err, graph.ErrNotFound) {
		t.Fatal(err)
	}
	if err := store.PutChangeNode(t.Context(), bad, n); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	if err := store.PutChangeLink(t.Context(), bad, l); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	bad = s
	bad.Access.TenantID = "foreign"
	if err := store.PutChangeNode(t.Context(), bad, n); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.GetChangeNode(ctx, s, f.Ref); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := store.ChangeNeighbors(t.Context(), s, graph.ChangeQuery{}); !errors.Is(err, graph.ErrInvalidGraph) {
		t.Fatal(err)
	}
}
