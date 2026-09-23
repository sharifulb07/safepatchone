package sir_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/sir"
)

func fields() sir.Fields {
	return sir.Fields{Metadata: sir.Metadata{Schema: sir.Schema, TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "sha123"}, Producer: "extractor", ProducerVersion: "1.0.0", Deterministic: true, Coverage: sir.CoveragePartial}, Files: []sir.SourceFile{{Path: "src/main.go", ContentHash: evidence.Digest([]byte("source"))}}, Nodes: []sir.Node{{Concept: sir.Concept{ID: "fn", Language: "go", NativeKind: "func_decl", Name: "Main", Source: evidence.SourceLocation{Path: "src/main.go", StartLine: 1, StartColumn: 1, EndLine: 3, EndColumn: 1}}, Kind: sir.Function}}}
}

func TestNormalizerCoversAllSharedKindsAcrossLanguages(t *testing.T) {
	kinds := sir.Kinds()
	if len(kinds) != 13 {
		t.Fatal(kinds)
	}
	var mappings []sir.Mapping
	in := sir.Extraction{Metadata: fields().Metadata, Files: fields().Files}
	for _, language := range []string{"go", "typescript", "python"} {
		for _, kind := range kinds {
			native := "adapter_" + string(kind)
			mappings = append(mappings, sir.Mapping{Language: language, NativeKind: native, Kind: kind})
			c := fields().Nodes[0].Concept
			c.ID = language + "_" + string(kind)
			c.Language = language
			c.NativeKind = native
			switch kind {
			case sir.DBOperation, sir.NetworkOperation, sir.FilesystemOperation, sir.ProcessOperation, sir.ConcurrencyOperation, sir.SideEffect:
				c.Action = "observe"
			}
			in.Concepts = append(in.Concepts, c)
		}
	}
	n, err := sir.NewNormalizer(mappings)
	if err != nil {
		t.Fatal(err)
	}
	mappings[0].Kind = "enterprise_function"
	r, err := n.Normalize(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := r.Fields()
	if len(f.Nodes) != 39 || f.Coverage != sir.CoveragePartial {
		t.Fatal(f)
	}
	for _, node := range f.Nodes {
		if !node.Kind.Valid() || node.NativeKind != "adapter_"+string(node.Kind) {
			t.Fatal(node)
		}
	}
	// A real adapter mapping can use distinct native names for the same kind.
	n, _ = sir.NewNormalizer([]sir.Mapping{{Language: "go", NativeKind: "func_decl", Kind: sir.Function}, {Language: "typescript", NativeKind: "function_declaration", Kind: sir.Function}})
	in.Concepts = []sir.Concept{fields().Nodes[0].Concept}
	first, err := n.Normalize(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.Concepts[0].Language = "typescript"
	in.Concepts[0].NativeKind = "function_declaration"
	second, err := n.Normalize(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.Fields()
	b, _ := second.Fields()
	if a.Nodes[0].Kind != b.Nodes[0].Kind || a.Nodes[0].Language == b.Nodes[0].Language {
		t.Fatal("normalization lost kind or provenance")
	}
}

func TestCanonicalImmutabilityAndReferences(t *testing.T) {
	f := fields()
	child := f.Nodes[0]
	child.ID = "child"
	child.ParentID = "fn"
	child.TargetID = "fn"
	f.Nodes = append(f.Nodes, child)
	f.Nodes[0].TargetID = "child" // Recursive reference cycles are allowed.
	r, err := sir.New(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := r.MarshalJSON()
	hash := r.ContentHash()
	slices.Reverse(f.Nodes)
	reordered, err := sir.New(t.Context(), f)
	if err != nil || reordered.ContentHash() != hash {
		t.Fatal("ordering changed identity", err)
	}
	f.Nodes[0].Name = "mutated"
	f.Files[0].ContentHash = evidence.Digest(nil)
	copyFields, _ := r.Fields()
	copyFields.Nodes[0].Source.Path = "other"
	copyFields.Files[0].Path = "other"
	copyRaw, _ := r.MarshalJSON()
	copyRaw[0] = '!'
	after, _ := r.MarshalJSON()
	if !bytes.Equal(raw, after) || r.ContentHash() != hash {
		t.Fatal("mutable representation")
	}
	restored, err := sir.Decode(t.Context(), raw)
	if err != nil || restored.ContentHash() != hash {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &r); !errors.Is(err, sir.ErrImmutableRepresentation) || r.ContentHash() != hash {
		t.Fatal(err)
	}
	for _, edit := range []func(*sir.Fields){func(f *sir.Fields) { f.TenantID = "other" }, func(f *sir.Fields) { f.Subject.RepositoryID = "other" }, func(f *sir.Fields) { f.Subject.Revision = "other" }, func(f *sir.Fields) { f.ProducerVersion = "2.0.0" }, func(f *sir.Fields) { f.Files[0].ContentHash = evidence.Digest([]byte("changed")) }} {
		copy, _ := r.Fields()
		edit(&copy)
		changed, err := sir.New(t.Context(), copy)
		if err != nil || changed.ContentHash() == hash {
			t.Fatal("unbound identity", err)
		}
	}
}

func TestInvalidSIRAndReferencePoisoning(t *testing.T) {
	for _, scenario := range []string{"edition_kind", "empty_schema", "empty_coverage", "pass_coverage", "bad_tenant", "bad_revision", "bad_version", "traversal", "symlink_style_path", "foreign_source", "bad_hash", "duplicate_file", "duplicate_node", "dangling_parent", "dangling_target", "self_parent", "cycle", "operation_parent", "bad_span", "bad_column", "control", "format_control", "unicode", "empty_name", "unknown_action", "missing_action", "oversized_nodes", "oversized_files", "oversized_name", "oversized_json"} {
		t.Run(scenario, func(t *testing.T) {
			f := fields()
			switch scenario {
			case "edition_kind":
				f.Nodes[0].Kind = "enterprise_function"
			case "empty_schema":
				f.Schema = ""
			case "empty_coverage":
				f.Coverage = ""
			case "pass_coverage":
				f.Coverage = "PASS"
			case "bad_tenant":
				f.TenantID = "../org"
			case "bad_revision":
				f.Subject.Revision = "../../secret"
			case "bad_version":
				f.ProducerVersion = "latest"
			case "traversal":
				f.Files[0].Path = "../secret"
				f.Nodes[0].Source.Path = "../secret"
			case "symlink_style_path":
				f.Nodes[0].Source.Path = "link/../secret"
			case "foreign_source":
				f.Nodes[0].Source.Path = "unlisted/file.go"
			case "bad_hash":
				f.Files[0].ContentHash = "secret"
			case "duplicate_file":
				f.Files = append(f.Files, f.Files[0])
			case "duplicate_node":
				f.Nodes = append(f.Nodes, f.Nodes[0])
			case "dangling_parent":
				f.Nodes[0].ParentID = "foreign"
			case "dangling_target":
				f.Nodes[0].TargetID = "foreign"
			case "self_parent":
				f.Nodes[0].ParentID = "fn"
			case "cycle", "operation_parent":
				child := f.Nodes[0]
				child.ID = "child"
				child.ParentID = "fn"
				f.Nodes = append(f.Nodes, child)
				if scenario == "cycle" {
					f.Nodes[0].ParentID = "child"
				} else {
					f.Nodes[0].Kind = sir.ProcessOperation
					f.Nodes[0].Action = "spawn"
				}
			case "bad_span":
				f.Nodes[0].Source.EndLine = -1
			case "bad_column":
				f.Nodes[0].Source.StartLine = 0
			case "control":
				f.Nodes[0].Name = "secret\x1b[31m"
			case "format_control":
				f.Nodes[0].Name = "hidden\u202e"
			case "unicode":
				f.Nodes[0].Name = string([]byte{0xff})
			case "empty_name":
				f.Nodes[0].Name = "   "
			case "unknown_action":
				f.Nodes[0].Action = "execute"
			case "missing_action":
				f.Nodes[0].Kind = sir.DBOperation
			case "oversized_nodes":
				f.Nodes = make([]sir.Node, sir.MaxNodes+1)
			case "oversized_files":
				f.Files = make([]sir.SourceFile, sir.MaxFiles+1)
			case "oversized_name":
				f.Nodes[0].Name = strings.Repeat("x", 1025)
			case "oversized_json":
				base := f.Nodes[0]
				f.Nodes = nil
				for i := range sir.MaxNodes {
					n := base
					n.ID = "n" + strconv.Itoa(i)
					n.Name = strings.Repeat("x", 1024)
					f.Nodes = append(f.Nodes, n)
				}
			}
			r, err := sir.New(t.Context(), f)
			if !errors.Is(err, sir.ErrInvalidRepresentation) || r.ContentHash() != "" || strings.Contains(err.Error(), "secret") {
				t.Fatal(r, err)
			}
		})
	}
}

func TestStrictDecodeRejectsCommercialAndUnknownFields(t *testing.T) {
	r, _ := sir.New(t.Context(), fields())
	raw, _ := r.MarshalJSON()
	for _, bad := range [][]byte{
		nil, []byte(`{}`), []byte(`null`), append(bytes.Clone(raw), []byte(`{}`)...), bytes.Repeat([]byte("x"), sir.MaxBytes+1),
		bytes.Replace(raw, []byte(`"schema":`), []byte(`"edition":"Enterprise","schema":`), 1),
		bytes.Replace(raw, []byte(`"kind":"function"`), []byte(`"entitlement":"intent.advanced","kind":"function"`), 1),
		bytes.Replace(raw, []byte(`"coverage":"PARTIAL"`), []byte(`"coverage":"PARTIAL","coverage":"COMPLETE"`), 1),
		bytes.Replace(raw, []byte(`"nodes":[`), []byte(`"Nodes":[`), 1),
		bytes.Replace(raw, []byte(`"target_id":""`), []byte(`"target_id":null`), 1),
		bytes.Replace(raw, []byte(`"deterministic":true`), []byte(`"deterministic":"true"`), 1),
		bytes.Replace(raw, []byte(`"nodes":[`), []byte(`"nodes":[null,`), 1),
		bytes.Replace(raw, []byte(`"start_line":1`), []byte(`"Start_line":1`), 1),
		bytes.Replace(raw, []byte(`"name":"Main"`), []byte(`"name":"\ud800"`), 1),
	} {
		if _, err := sir.Decode(t.Context(), bad); err == nil {
			t.Fatal("accepted ambiguous or unsafe SIR")
		}
	}
}

func TestUnknownCoverageAndUnsupportedConceptsFailClosed(t *testing.T) {
	mappings := []sir.Mapping{{Language: "go", NativeKind: "func_decl", Kind: sir.Function}}
	n, _ := sir.NewNormalizer(mappings)
	for _, coverage := range []sir.Coverage{sir.CoverageUnknown, sir.CoveragePartial, sir.CoverageComplete} {
		f := fields()
		f.Coverage = coverage
		f.Deterministic = false
		r, err := n.Normalize(t.Context(), sir.Extraction{Metadata: f.Metadata, Files: f.Files, Concepts: []sir.Concept{f.Nodes[0].Concept}})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := r.Fields()
		if got.Coverage != coverage || got.Deterministic {
			t.Fatal("promoted extraction claims")
		}
	}
	f := fields()
	f.Nodes[0].NativeKind = "unmapped"
	if r, err := n.Normalize(t.Context(), sir.Extraction{Metadata: f.Metadata, Files: f.Files, Concepts: []sir.Concept{f.Nodes[0].Concept}}); !errors.Is(err, sir.ErrUnknownConcept) || r.ContentHash() != "" {
		t.Fatal(r, err)
	}
	for _, maps := range [][]sir.Mapping{nil, append(mappings, mappings...), {{Language: "go", NativeKind: "function", Kind: "enterprise_function"}}, {{Language: "Go", NativeKind: "function", Kind: sir.Function}}, make([]sir.Mapping, sir.MaxMappings+1)} {
		if _, err := sir.NewNormalizer(maps); !errors.Is(err, sir.ErrInvalidMapping) {
			t.Fatal(err)
		}
	}
	for _, n := range []*sir.Normalizer{nil, {}} {
		if _, err := n.Normalize(t.Context(), sir.Extraction{}); !errors.Is(err, sir.ErrInvalidMapping) {
			t.Fatal(err)
		}
	}
}

func TestDataIsNotExecutedAndContextsAreHonored(t *testing.T) {
	f := fields()
	f.Nodes[0].Kind = sir.ProcessOperation
	f.Nodes[0].Action = "spawn"
	f.Nodes[0].Name = "$(echo secret)"
	f.Nodes[0].Target = "https://169.254.169.254/; rm -rf /"
	r, err := sir.New(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := r.MarshalJSON()
	if !bytes.Contains(raw, []byte("169.254.169.254")) {
		t.Fatal("lost inert target")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	n, _ := sir.NewNormalizer([]sir.Mapping{{Language: "go", NativeKind: "func_decl", Kind: sir.Function}})
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := sir.New(ctx, f); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := sir.Decode(ctx, raw); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := n.Normalize(ctx, sir.Extraction{}); err == nil {
			t.Fatal("context ignored")
		}
	}
	if _, err := (sir.Representation{}).Fields(); err == nil {
		t.Fatal("invalid zero value")
	}
	if _, err := (sir.Representation{}).MarshalJSON(); err == nil {
		t.Fatal("invalid zero value")
	}
}

func TestDeepContainmentAndConcurrentReads(t *testing.T) {
	f := fields()
	base := f.Nodes[0]
	f.Nodes = nil
	for i := range sir.MaxNodes {
		n := base
		n.ID = "n" + strconv.Itoa(i)
		if i > 0 {
			n.ParentID = "n" + strconv.Itoa(i-1)
		}
		f.Nodes = append(f.Nodes, n)
	}
	r, err := sir.New(t.Context(), f)
	if err != nil {
		t.Fatal("deep iterative validation", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			raw, err := r.MarshalJSON()
			if err != nil {
				t.Error(err)
				return
			}
			restored, err := sir.Decode(t.Context(), raw)
			if err != nil || restored.ContentHash() != r.ContentHash() {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func FuzzDecodeSIR(f *testing.F) {
	r, _ := sir.New(context.Background(), fields())
	raw, _ := r.MarshalJSON()
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := sir.Decode(t.Context(), raw)
		if err != nil {
			return
		}
		canonical, err := r.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		again, err := sir.Decode(t.Context(), canonical)
		if err != nil || again.ContentHash() != r.ContentHash() {
			t.Fatal("unstable SIR", err)
		}
	})
}
