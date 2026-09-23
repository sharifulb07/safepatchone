package testtrust_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/testtrust"
)

func fields(revision string) testtrust.Fields {
	hash := evidence.Digest([]byte("fixture"))
	return testtrust.Fields{Schema: "safepatch.testtrust-snapshot.v1", OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: revision}, Producer: "fixture", ProducerVersion: "1.0.0", SourceDigest: hash, Complete: true, Tests: []testtrust.Test{{ID: "test", Source: evidence.SourceLocation{Path: "a_test.go"}, Complete: true, Skip: "ENABLED", Negative: "NEGATIVE", Assertions: []testtrust.Assertion{{ID: "assertion", SubjectDigest: hash, PredicateDigest: hash, Relation: "EQ", Bound: "10"}}}}, Coverage: testtrust.Coverage{Known: true, BasisDigest: hash, Covered: 90, Total: 100}}
}
func seal(t *testing.T, f testtrust.Fields) testtrust.Snapshot {
	t.Helper()
	s, err := testtrust.New(f)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func has(r testtrust.Result, code string) bool {
	for _, f := range r.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}
func TestSixDetectionsAndIncompleteEvidence(t *testing.T) {
	for _, mode := range []string{"unchanged", "removed_test", "removed_assertion", "weakened", "skipped", "coverage", "negative", "partial", "missing_coverage", "different_basis", "unknown_assertion", "incomparable_assertion"} {
		t.Run(mode, func(t *testing.T) {
			before, after := fields("base"), fields("head")
			code := ""
			want := testtrust.Regression
			switch mode {
			case "unchanged":
				want = testtrust.NoRegression
			case "removed_test":
				after.Tests = []testtrust.Test{}
				code = "REMOVED_TEST"
			case "removed_assertion":
				after.Tests[0].Assertions = []testtrust.Assertion{}
				code = "REMOVED_ASSERTION"
			case "weakened":
				after.Tests[0].Assertions[0].Relation = "GE"
				code = "WEAKENED_ASSERTION"
			case "skipped":
				after.Tests[0].Skip = "SKIPPED"
				code = "SKIPPED_TEST"
			case "coverage":
				after.Coverage.Covered = 89
				code = "COVERAGE_REDUCTION"
			case "negative":
				after.Tests[0].Negative = "POSITIVE"
				code = "NEGATIVE_TEST_REMOVED"
			case "partial":
				after.Complete = false
				after.Tests = []testtrust.Test{}
				want = testtrust.Unknown
			case "missing_coverage":
				after.Coverage = testtrust.Coverage{}
				want = testtrust.Unknown
			case "different_basis":
				after.Coverage.BasisDigest = evidence.Digest([]byte("different"))
				want = testtrust.Unknown
			case "unknown_assertion":
				after.Tests[0].Assertions[0].Relation = "UNKNOWN"
				after.Tests[0].Assertions[0].Bound = ""
				want = testtrust.Unknown
			case "incomparable_assertion":
				after.Tests[0].Assertions[0].Bound = "20"
				code = "ASSERTION_CHANGE_UNCLASSIFIED"
				want = testtrust.Unknown
			}
			r, err := (testtrust.Engine{}).Compare(t.Context(), seal(t, before), seal(t, after))
			if err != nil || r.Decision != want || code != "" && !has(r, code) || r.MutationStatus != "NOT_RUN" {
				t.Fatal(r, err)
			}
			if mode == "removed_test" && !has(r, "NEGATIVE_TEST_REMOVED") {
				t.Fatal("negative removal missing")
			}
			if mode == "partial" && has(r, "REMOVED_TEST") {
				t.Fatal("incomplete scan inferred deletion")
			}
		})
	}
}
func TestNumericWeakeningIsImplicationNotTextChange(t *testing.T) {
	for _, tc := range []struct {
		oldRel, oldBound, newRel, newBound string
		want                               testtrust.Decision
	}{{"GT", "10", "GE", "10", testtrust.Regression}, {"GE", "10", "GE", "9", testtrust.Regression}, {"LT", "10", "LE", "10", testtrust.Regression}, {"EQ", "0.1", "EQ", "0.10", testtrust.NoRegression}, {"GE", "9", "GE", "10", testtrust.NoRegression}, {"GE", "10", "LT", "10", testtrust.Unknown}, {"EQ", "9007199254740993", "GE", "9007199254740992", testtrust.Regression}, {"NE", "1", "NE", "2", testtrust.Unknown}} {
		a, b := fields("base"), fields("head")
		a.Tests[0].Assertions[0].Relation = tc.oldRel
		a.Tests[0].Assertions[0].Bound = tc.oldBound
		b.Tests[0].Assertions[0].Relation = tc.newRel
		b.Tests[0].Assertions[0].Bound = tc.newBound
		r, err := (testtrust.Engine{}).Compare(t.Context(), seal(t, a), seal(t, b))
		if err != nil || r.Decision != tc.want {
			t.Fatal(tc, r, err)
		}
	}
}
func TestImmutableSnapshotsAndPoisoning(t *testing.T) {
	s := seal(t, fields("base"))
	raw, _ := json.Marshal(s)
	f, _ := s.Fields()
	f.Tests[0].Assertions[0].Bound = "999"
	after, _ := json.Marshal(s)
	if !bytes.Equal(raw, after) {
		t.Fatal("snapshot mutated")
	}
	decoded, err := testtrust.Decode(raw)
	if err != nil || decoded.Digest() != s.Digest() {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"x","schema":`), 1), bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1), append([]byte(`{"unknown":true,`), raw[1:]...), bytes.Repeat([]byte("x"), testtrust.MaxBytes+1)} {
		if _, err := testtrust.Decode(bad); err == nil {
			t.Fatal("poison accepted")
		}
	}
	for _, mode := range []string{"path", "duplicate", "overflow", "bound", "empty_null"} {
		f := fields("base")
		switch mode {
		case "path":
			f.Tests[0].Source.Path = "../secret"
		case "duplicate":
			f.Tests = append(f.Tests, f.Tests[0])
		case "overflow":
			f.Coverage.Total = 1_000_000_001
		case "bound":
			f.Tests[0].Assertions[0].Bound = "1e999999"
		case "empty_null":
			f.Tests = nil
		}
		if _, err := testtrust.New(f); err == nil {
			t.Fatal(mode)
		}
	}
}
func TestScopeProducerAndCancellation(t *testing.T) {
	for _, mode := range []string{"organization", "repository", "producer", "revision", "cancel"} {
		a, b := fields("base"), fields("head")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		switch mode {
		case "organization":
			b.OrganizationID = "other"
		case "repository":
			b.Subject.RepositoryID = "other"
		case "producer":
			b.ProducerVersion = "2.0.0"
		case "revision":
			b.Subject.Revision = "base"
			b.SourceDigest = evidence.Digest([]byte("altered"))
		case "cancel":
			cancel()
		}
		r, err := (testtrust.Engine{}).Compare(ctx, seal(t, a), seal(t, b))
		if err == nil || r.Decision != testtrust.Unknown {
			t.Fatal(mode, r, err)
		}
	}
}
func FuzzSnapshot(f *testing.F) {
	s, _ := testtrust.New(fields("base"))
	raw, _ := json.Marshal(s)
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		s, err := testtrust.Decode(raw)
		if err == nil {
			b, _ := json.Marshal(s)
			round, err := testtrust.Decode(b)
			if err != nil || round.Digest() != s.Digest() {
				t.Fatal("unstable snapshot")
			}
		}
	})
}
