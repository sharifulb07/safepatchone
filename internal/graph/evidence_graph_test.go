package graph_test

import (
	"slices"
	"testing"
	"time"

	"safepatch/internal/graph"
)

func TestRelationshipContract(t *testing.T) {
	want := []graph.Relationship{"SUPPORTS", "CONTRADICTS", "DERIVED_FROM", "VALIDATES", "INVALIDATES", "DEPENDS_ON"}
	if !slices.Equal(graph.Relationships(), want) {
		t.Fatal("relationship contract changed")
	}
	copy := graph.Relationships()
	copy[0] = "PASS"
	for _, r := range want {
		e := graph.Edge{TenantID: "org", EdgeID: "edge", FromEvidenceID: "a", ToEvidenceID: "b", Relationship: r, CreatedAt: time.Now()}
		canonical, err := e.Canonical()
		if err != nil || canonical.Relationship != r || canonical.CreatedAt.Nanosecond()%1000 != 0 {
			t.Fatal(canonical, err)
		}
		// Reversed assertions and cycles are legal; they remain explicit edges.
		e.FromEvidenceID, e.ToEvidenceID = e.ToEvidenceID, e.FromEvidenceID
		if e.Validate() != nil {
			t.Fatal("reverse direction rejected")
		}
		e.ToEvidenceID = e.FromEvidenceID
		if e.Validate() == nil {
			t.Fatal("self edge accepted")
		}
	}
	for _, r := range []graph.Relationship{"", "UNKNOWN", "PASS", "supports"} {
		if r.Valid() {
			t.Fatal(r)
		}
	}
	nearZero := graph.Edge{TenantID: "org", EdgeID: "edge", FromEvidenceID: "a", ToEvidenceID: "b", Relationship: graph.Supports, CreatedAt: time.Time{}.Add(time.Nanosecond)}
	if _, err := nearZero.Canonical(); err == nil {
		t.Fatal("zero timestamp accepted after canonicalization")
	}
}

func TestScopeAndQuerySecurityBounds(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	valid := graph.Scope{TenantID: "org", RepositoryIDs: []string{"repo"}, NotBefore: now.AddDate(0, 0, -1), AsOf: now}
	if valid.Validate() != nil || !valid.Contains("repo", now.Add(999*time.Nanosecond)) || valid.Contains("other", now) || valid.Contains("repo", now.Add(time.Microsecond)) || valid.Contains("repo", valid.NotBefore.Add(-time.Microsecond)) {
		t.Fatal("scope boundary")
	}
	for _, change := range []func(*graph.Scope){
		func(s *graph.Scope) { s.TenantID = "org' OR true--" }, func(s *graph.Scope) { s.RepositoryIDs = nil },
		func(s *graph.Scope) { s.RepositoryIDs = []string{"../repo"} }, func(s *graph.Scope) { s.RepositoryIDs = []string{"repo", "repo"} },
		func(s *graph.Scope) { s.AsOf = time.Time{} }, func(s *graph.Scope) { s.NotBefore = s.AsOf.Add(time.Second) },
		func(s *graph.Scope) { s.AsOf = s.AsOf.Add(time.Nanosecond) }, func(s *graph.Scope) { s.RepositoryIDs = make([]string, graph.MaxRepositories+1) },
	} {
		s := valid
		change(&s)
		if s.Validate() == nil {
			t.Fatal("invalid scope accepted", s)
		}
	}
	for _, direction := range []graph.Direction{graph.Incoming, graph.Outgoing, graph.Both} {
		q := graph.Query{EvidenceID: "ev", Direction: direction, Limit: graph.MaxPageSize}
		if q.Validate() != nil {
			t.Fatal(q)
		}
		for _, change := range []func(*graph.Query){func(q *graph.Query) { q.Limit = 0 }, func(q *graph.Query) { q.Limit++ }, func(q *graph.Query) { q.AfterEdgeID = "'" }, func(q *graph.Query) { q.Direction = "ALL" }, func(q *graph.Query) { q.Relationship = "UNKNOWN" }, func(q *graph.Query) { q.EvidenceID = "../secret" }} {
			bad := q
			change(&bad)
			if bad.Validate() == nil {
				t.Fatal(bad)
			}
		}
	}
}
