package testtrustgo_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/platform/testtrustgo"
	"safepatch/internal/proof/testtrust"
)

const baseline = `package example
import "testing"
// safepatch:testtrust negative
func TestReject(t *testing.T) { got := 10; if got != 10 { t.Fatal("secret credential") } }
`

func extract(t *testing.T, source, revision string, covered int64) testtrust.Snapshot {
	t.Helper()
	s, err := testtrustgo.Extract(t.Context(), "org", evidence.Subject{RepositoryID: "repo", Revision: revision}, []testtrustgo.File{{Path: "a_test.go", Content: []byte(source)}}, true, testtrust.Coverage{Known: true, BasisDigest: evidence.Digest([]byte("coverage-config")), Covered: covered, Total: 100})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestGoSourceToSharedDetections(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		covered            int64
	}{{"removed_test", "package example", "REMOVED_TEST", 90}, {"negative_removed", "package example", "NEGATIVE_TEST_REMOVED", 90}, {"assertion_removed", strings.Replace(baseline, `if got != 10 { t.Fatal("secret credential") }`, `_ = got`, 1), "REMOVED_ASSERTION", 90}, {"weakened", strings.Replace(baseline, "got != 10", "got < 10", 1), "WEAKENED_ASSERTION", 90}, {"skip", strings.Replace(baseline, "got := 10", `t.Skip("private"); got := 10`, 1), "SKIPPED_TEST", 90}, {"coverage", baseline, "COVERAGE_REDUCTION", 80}} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := extract(t, baseline, "base", 90), extract(t, tc.source, "head", tc.covered)
			r, err := (testtrust.Engine{}).Compare(t.Context(), a, b)
			found := false
			for _, f := range r.Findings {
				found = found || f.Code == tc.code
			}
			if err != nil || !found || r.Decision != testtrust.Regression {
				t.Fatal(r, err)
			}
			raw, _ := json.Marshal(a)
			if bytes.Contains(raw, []byte("secret credential")) {
				t.Fatal("source literal leaked")
			}
		})
	}
}
func TestUnsupportedGoCannotBecomeClean(t *testing.T) {
	for _, source := range []string{strings.Replace(baseline, `got != 10`, `got != expected`, 1), strings.Replace(baseline, `got := 10`, `got := compute()`, 1), strings.Replace(baseline, `got := 10`, `if false { return }; got := 10`, 1), strings.Replace(baseline, `got := 10`, `t.Run("sub",func(t *testing.T){t.Skip("skip")}); got := 10`, 1), strings.Replace(baseline, `// safepatch:testtrust negative`, "", 1)} {
		a, b := extract(t, source, "base", 90), extract(t, source, "head", 90)
		r, err := (testtrust.Engine{}).Compare(t.Context(), a, b)
		if err != nil || r.Decision != testtrust.Unknown {
			t.Fatal(r, err)
		}
	}
}
func TestGoErrorsBoundsAndShadowedReceiver(t *testing.T) {
	for _, files := range [][]testtrustgo.File{{{Path: "../a_test.go", Content: []byte(baseline)}}, {{Path: "a_test.go", Content: []byte("invalid")}}, {{Path: "a_test.go", Content: bytes.Repeat([]byte("x"), testtrust.MaxBytes+1)}}, {{Path: "a_test.go", Content: []byte(baseline)}, {Path: "a_test.go", Content: []byte(baseline)}}} {
		if _, err := testtrustgo.Extract(t.Context(), "org", evidence.Subject{RepositoryID: "repo", Revision: "base"}, files, true, testtrust.Coverage{}); err == nil {
			t.Fatal("unsafe source accepted")
		}
	}
	source := strings.Replace(baseline, `got := 10`, `t := fake(); got := 10`, 1)
	a := extract(t, source, "base", 90)
	f, _ := a.Fields()
	if f.Tests[0].Complete {
		t.Fatal("shadowed receiver trusted")
	}
	a = extract(t, strings.Replace(baseline, "TestReject", "Testhelper", 1), "base", 90)
	f, _ = a.Fields()
	if len(f.Tests) != 0 {
		t.Fatal("non-test discovered")
	}
	a = extract(t, strings.Replace(baseline, "t *testing.T", "_ *testing.T", 1), "base", 90)
	f, _ = a.Fields()
	if f.Complete {
		t.Fatal("unbound testing receiver treated as complete")
	}
}
