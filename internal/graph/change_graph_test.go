package graph_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
)

func changeFields() graph.ChangeNodeFields {
	return graph.ChangeNodeFields{Schema: graph.ChangeNodeSchema, TenantID: "org", Ref: graph.EntityRef{RepositoryID: "repo", Revision: "rev1", EntityID: "main"}, Kind: graph.FileEntity, Name: "main.go", Path: "src/main.go", InputHash: evidence.Digest([]byte("source")), Payload: json.RawMessage(`{"z":1,"a":{"n":9007199254740993}}`), CreatedAt: time.Date(2026, 9, 6, 12, 0, 0, 999, time.UTC)}
}

func TestChangeNodeCanonicalImmutableAndRevisionIdentity(t *testing.T) {
	f := changeFields()
	for _, kind := range graph.EntityKinds() {
		f.Kind = kind
		n, err := graph.NewChangeNode(f)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := n.MarshalJSON()
		decoded, err := graph.DecodeChangeNode(raw)
		if err != nil || decoded.ContentHash() != n.ContentHash() {
			t.Fatal(err)
		}
		copyFields, _ := n.Fields()
		copyFields.Payload[0] = '!'
		f.Payload = json.RawMessage(`{"a":{"n":9007199254740993},"z":1}`)
		same, err := graph.NewChangeNode(f)
		if err != nil || same.ContentHash() != n.ContentHash() {
			t.Fatal("unstable canonical hash", err)
		}
		changed := f
		changed.Ref.Revision = "rev2"
		next, _ := graph.NewChangeNode(changed)
		if next.ContentHash() == n.ContentHash() {
			t.Fatal("revision omitted from identity")
		}
		if !bytes.Contains(raw, []byte("9007199254740993")) {
			t.Fatal("precision loss")
		}
		if err := json.Unmarshal(raw, &n); err == nil {
			t.Fatal("mutated sealed node")
		}
		if n.ContentHash() != same.ContentHash() {
			t.Fatal("mutation escaped")
		}
	}
	n, _ := graph.NewChangeNode(f)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			for j := 0; j < 20; j++ {
				f, _ := n.Fields()
				f.Payload[0] = '!'
				_, _ = n.MarshalJSON()
				_ = n.ContentHash()
			}
		})
	}
	wg.Wait()
}

func TestChangeNodeUntrustedInputs(t *testing.T) {
	mutations := map[string]func(*graph.ChangeNodeFields){
		"tenant":     func(f *graph.ChangeNodeFields) { f.TenantID = "../org" },
		"revision":   func(f *graph.ChangeNodeFields) { f.Ref.Revision = "../../rev" },
		"kind":       func(f *graph.ChangeNodeFields) { f.Kind = "enterprise" },
		"schema":     func(f *graph.ChangeNodeFields) { f.Schema = "future" },
		"traversal":  func(f *graph.ChangeNodeFields) { f.Path = "../secret" },
		"absolute":   func(f *graph.ChangeNodeFields) { f.Path = "C:/secret" },
		"backslash":  func(f *graph.ChangeNodeFields) { f.Path = `src\..\secret` },
		"empty_path": func(f *graph.ChangeNodeFields) { f.Path = "" },
		"control":    func(f *graph.ChangeNodeFields) { f.Name = "secret\n" },
		"hash":       func(f *graph.ChangeNodeFields) { f.InputHash = "unknown" },
		"timestamp":  func(f *graph.ChangeNodeFields) { f.CreatedAt = time.Time{} },
		"duplicate":  func(f *graph.ChangeNodeFields) { f.Payload = json.RawMessage(`{"a":1,"a":2}`) },
		"nul":        func(f *graph.ChangeNodeFields) { f.Payload = json.RawMessage(`{"a":"\u0000"}`) },
		"size": func(f *graph.ChangeNodeFields) {
			f.Payload = json.RawMessage(`{"a":"` + strings.Repeat("a", graph.MaxChangeObjectBytes) + `"}`)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := changeFields()
			mutate(&f)
			if _, err := graph.NewChangeNode(f); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
	n, _ := graph.NewChangeNode(changeFields())
	raw, _ := n.MarshalJSON()
	for _, raw := range [][]byte{nil, []byte(`{}`), append([]byte(`{"unknown":1,`), raw[1:]...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"duplicate","schema":`), 1), bytes.Replace(raw, []byte(`"ref":{`), []byte(`"ref":{"extra":1,`), 1)} {
		if _, err := graph.DecodeChangeNode(raw); err == nil {
			t.Fatal("accepted poisoned JSON")
		}
	}
}

func TestRevisionScopeRequiresExactPinsAndTimeWindow(t *testing.T) {
	now := changeFields().CreatedAt.Truncate(time.Microsecond)
	scope := graph.RevisionScope{Access: graph.Scope{TenantID: "org", RepositoryIDs: []string{"repo", "other"}, NotBefore: now.Add(-time.Hour), AsOf: now}, Revisions: []graph.RevisionPin{{RepositoryID: "repo", Revision: "rev1"}, {RepositoryID: "other", Revision: "rev9"}}}
	if scope.Validate() != nil || !scope.Includes(changeFields().Ref, now) {
		t.Fatal("valid scope denied")
	}
	for _, ref := range []graph.EntityRef{{RepositoryID: "repo", Revision: "rev2", EntityID: "main"}, {RepositoryID: "foreign", Revision: "rev1", EntityID: "main"}} {
		if scope.Includes(ref, now) {
			t.Fatal("scope escape")
		}
	}
	if scope.Includes(changeFields().Ref, now.Add(time.Second)) || scope.Includes(changeFields().Ref, now.Add(-2*time.Hour)) {
		t.Fatal("retention escape")
	}
	for _, pins := range [][]graph.RevisionPin{nil, {{RepositoryID: "repo", Revision: "rev1"}}, {{RepositoryID: "repo", Revision: "rev1"}, {RepositoryID: "repo", Revision: "rev2"}}, {{RepositoryID: "repo", Revision: "rev1"}, {RepositoryID: "foreign", Revision: "rev9"}}} {
		s := scope
		s.Revisions = pins
		if s.Validate() == nil {
			t.Fatal("ambiguous pins accepted")
		}
	}
}

func TestCausalLinksPreserveBasisAndRejectMixedRevisions(t *testing.T) {
	f := graph.ChangeLinkFields{Schema: graph.ChangeLinkSchema, TenantID: "org", LinkID: "link1", From: changeFields().Ref, To: graph.EntityRef{RepositoryID: "other", Revision: "rev9", EntityID: "handler"}, Relation: graph.Causes, Basis: graph.BasisUnknown, EvidenceID: "evidence1", CreatedAt: changeFields().CreatedAt}
	for _, relation := range graph.CausalRelations() {
		for _, basis := range []graph.CausalBasis{graph.BasisObserved, graph.BasisInferred, graph.BasisUnknown} {
			f.Relation = relation
			f.Basis = basis
			n, err := graph.NewChangeLink(f)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := n.MarshalJSON()
			decoded, err := graph.DecodeChangeLink(raw)
			if err != nil || decoded.ContentHash() != n.ContentHash() {
				t.Fatal(err)
			}
			fields, _ := decoded.Fields()
			if fields.Basis != basis {
				t.Fatal("basis changed")
			}
			if json.Unmarshal(raw, &n) == nil {
				t.Fatal("mutable link")
			}
		}
	}
	for _, mode := range []string{"self", "mixed", "relation", "basis", "evidence", "tenant", "time"} {
		bad := f
		switch mode {
		case "self":
			bad.To = bad.From
		case "mixed":
			bad.To.RepositoryID = bad.From.RepositoryID
		case "relation":
			bad.Relation = "PASS"
		case "basis":
			bad.Basis = "PASS"
		case "evidence":
			bad.EvidenceID = ""
		case "tenant":
			bad.TenantID = "../org"
		case "time":
			bad.CreatedAt = time.Time{}
		}
		if _, err := graph.NewChangeLink(bad); err == nil {
			t.Fatal(mode)
		}
	}
	q := graph.ChangeQuery{Root: f.From, Direction: graph.Both, Limit: graph.MaxPageSize}
	if q.Validate() != nil {
		t.Fatal("valid query")
	}
	q.Limit++
	if q.Validate() == nil {
		t.Fatal("unbounded query")
	}
	q.Limit = 1
	q.AfterLinkID = "x' OR 1=1"
	if q.Validate() == nil {
		t.Fatal("unsafe cursor")
	}
}
