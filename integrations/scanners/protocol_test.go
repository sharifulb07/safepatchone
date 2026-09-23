package scanners

import (
	"strings"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

func TestIncompleteInventoriesAndProtocols(t *testing.T) {
	for _, tc := range []struct {
		tool   Tool
		raw    string
		status evidence.Status
		bad    bool
	}{
		{Semgrep, `{"version":"1.130.0","results":[],"errors":[],"paths":{"scanned":[]}}`, evidence.StatusSkipped, false},
		{Semgrep, `{"version":"1.130.0","results":[],"errors":[{"type":"Timeout"}],"paths":{"scanned":["main.go"]}}`, evidence.StatusUnknown, false},
		{Semgrep, `{"version":"1.130.0","results":[],"errors":[],"paths":{"scanned":["main.go"],"skipped":[{"path":"other.go"}]}}`, evidence.StatusUnknown, false},
		{Semgrep, strings.Replace(clean[Semgrep], "1.130.0", "99.0.0", 1), evidence.StatusError, true},
		{Trivy, `{"SchemaVersion":2,"ArtifactType":"filesystem","Results":[]}`, evidence.StatusSkipped, false},
		{Trivy, `{"SchemaVersion":2,"ArtifactType":"filesystem","Results":[{"Target":"go.mod","Packages":[{}]}]}`, evidence.StatusError, true},
		{Trivy, strings.Replace(clean[Trivy], `"SchemaVersion":2`, `"SchemaVersion":99`, 1), evidence.StatusError, true},
		{OSV, `{"results":[]}`, evidence.StatusSkipped, false},
		{OSV, `{"results":[{"source":{"path":"go.mod","type":"lockfile"},"packages":[null]}]}`, evidence.StatusError, true},
		{Syft, strings.Replace(clean[Syft], "16.0.0", "99.0.0", 1), evidence.StatusError, true},
		{Syft, `{"schema":{"version":"16.0.0"},"descriptor":{"name":"syft","version":"1.33.0"},"source":{"type":"directory"},"artifacts":[]}`, evidence.StatusSkipped, false},
	} {
		a, input := fixture(t, tc.tool)
		registry, _ := verification.NewRegistry(a)
		r, err := registry.Run(t.Context(), a.ID(), input, output(tc.raw, 0))
		if (err != nil) != tc.bad || r.Report.Status != tc.status {
			t.Fatal(tc.tool, tc.raw, r, err)
		}
	}
}

func TestScannerPathAndFindingBounds(t *testing.T) {
	a, input := fixture(t, Gitleaks)
	registry, _ := verification.NewRegistry(a)
	for _, p := range []string{"../secret", "/etc/passwd", `C:\\secret`, "./main.go"} {
		raw := `[{"RuleID":"secret","File":"` + p + `","StartLine":1}]`
		r, err := registry.Run(t.Context(), a.ID(), input, output(raw, 1))
		if err != nil || r.Report.Status != evidence.StatusFail {
			t.Fatal(r, err)
		}
		want := ""
		if p == "./main.go" {
			want = "main.go"
		}
		if r.Report.Findings[0].Path != want {
			t.Fatal("unsafe path exposed", r)
		}
	}
	raw := "[" + strings.Repeat(`{"RuleID":"secret","File":"main.go","StartLine":1},`, evidence.MaxFindings) + `{"RuleID":"secret","File":"main.go","StartLine":1}]`
	if r, err := registry.Run(t.Context(), a.ID(), input, output(raw, 1)); err == nil || r.Report.Status == evidence.StatusPass {
		t.Fatal("finding overflow", r, err)
	}
}

func FuzzNormalizeScannerOutput(f *testing.F) {
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		f.Add(string(tool), []byte(clean[tool]))
	}
	f.Add("gitleaks", []byte("null"))
	f.Fuzz(func(t *testing.T, name string, raw []byte) {
		tool := Tool(name)
		if _, ok := versions[tool]; !ok || len(raw) > 65536 {
			return
		}
		a, input := fixture(t, tool)
		for _, code := range []int{0, 1} {
			status := verification.Completed
			if code != 0 {
				status = verification.Failed
			}
			r, err := a.Normalize(t.Context(), verification.NormalizationRequest{Target: input.Target, Capability: input.Capability, Results: []verification.StepResult{{StepID: "scan", Result: verification.ExecutionResult{Status: status, ExitCode: code, Stdout: raw}}}})
			if err == nil {
				if _, err := r.Canonical(); err != nil {
					t.Fatal("invalid normalized report", err)
				}
				if code != 0 && r.Status == evidence.StatusPass {
					t.Fatal("nonzero PASS")
				}
			}
		}
	})
}
