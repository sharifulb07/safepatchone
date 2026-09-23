package evidencestore

import (
	"bytes"
	"testing"

	"safepatch/internal/domain/edition"
	"safepatch/internal/evidence"
	"safepatch/internal/sir"
)

func TestSIRNormalizationStorageAndEditionIndependentHistory(t *testing.T) {
	s := scope()
	store, err := NewLocal(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	n, err := sir.NewNormalizer([]sir.Mapping{{Language: "go", NativeKind: "receiver_method", Kind: sir.Method}, {Language: "typescript", NativeKind: "class_declaration", Kind: sir.Class}, {Language: "go", NativeKind: "sql_query", Kind: sir.DBOperation}})
	if err != nil {
		t.Fatal(err)
	}
	in := sir.Extraction{Metadata: sir.Metadata{Schema: sir.Schema, TenantID: s.OrganizationID, Subject: s.Subject, Producer: "mixed-language-adapter", ProducerVersion: "1.0.0", Deterministic: true, Coverage: sir.CoveragePartial}, Files: []sir.SourceFile{{Path: "store.go", ContentHash: evidence.Digest([]byte("Go source"))}, {Path: "client.ts", ContentHash: evidence.Digest([]byte("TypeScript source"))}}, Concepts: []sir.Concept{
		{ID: "method", Language: "go", NativeKind: "receiver_method", Name: "Store.Load", Source: evidence.SourceLocation{Path: "store.go", StartLine: 1}},
		{ID: "query", Language: "go", NativeKind: "sql_query", Name: "Load receipt", Source: evidence.SourceLocation{Path: "store.go", StartLine: 2}, ParentID: "method", Action: "read", Target: "receipts"},
		{ID: "class", Language: "typescript", NativeKind: "class_declaration", Name: "ReceiptClient", Source: evidence.SourceLocation{Path: "client.ts", StartLine: 1}},
	}}
	r, err := n.Normalize(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := r.MarshalJSON()
	ref, err := store.Put(t.Context(), s, raw)
	if err != nil {
		t.Fatal(err)
	}
	// Edition changes affect admission outside this schema; existing facts and
	// source hashes are identical when read for any of the five offering labels.
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			stored, err := store.Get(t.Context(), s, ref)
			if err != nil || !bytes.Equal(raw, stored) {
				t.Fatal(err)
			}
			got, err := sir.Decode(t.Context(), stored)
			if err != nil || got.ContentHash() != r.ContentHash() {
				t.Fatal(err)
			}
			f, _ := got.Fields()
			if len(f.Nodes) != 3 || f.Nodes[2].Kind != sir.DBOperation || f.Coverage != sir.CoveragePartial {
				t.Fatal("lost normalized facts")
			}
		})
	}
	for _, dimension := range []string{"tenant", "repository", "revision"} {
		foreign := s
		switch dimension {
		case "tenant":
			foreign.OrganizationID = "other"
		case "repository":
			foreign.Subject.RepositoryID = "other"
		case "revision":
			foreign.Subject.Revision = "other"
		}
		if _, err := store.Get(t.Context(), foreign, ref); err == nil {
			t.Fatal("cross-scope SIR read", dimension)
		}
		if _, err := store.Put(t.Context(), foreign, raw); err == nil {
			t.Fatal("cross-scope SIR write", dimension)
		}
	}
	in.ProducerVersion = "1.1.0"
	updated, err := n.Normalize(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	newRaw, _ := updated.MarshalJSON()
	newRef, err := store.Put(t.Context(), s, newRaw)
	if err != nil || newRef == ref {
		t.Fatal("history overwritten", err)
	}
	old, err := store.Get(t.Context(), s, ref)
	if err != nil || !bytes.Equal(raw, old) {
		t.Fatal("previous SIR corrupted", err)
	}
}
