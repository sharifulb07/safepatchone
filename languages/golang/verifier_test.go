package golang

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

func fixture(t *testing.T) (*Verifier, verification.PlanRequest) {
	t.Helper()
	v, err := New(Config{GoTool: "go", StaticcheckTool: "staticcheck", GovulncheckTool: "govulncheck", Parallelism: 2, CGOEnabled: true, FuzzSeconds: 1, FuzzTargets: []FuzzTarget{{Module: ".", Package: ".", Function: "FuzzExample"}}, BenchmarkIterations: 1, VulnerabilityDB: "file:///trusted/vulndb"})
	if err != nil {
		t.Fatal(err)
	}
	r := verification.PlanRequest{Capability: Test, Target: verification.Target{Subject: evidence.Subject{RepositoryID: "repo", Revision: "revision"}, Files: []string{"go.mod", "main.go"}, Metadata: map[string]string{"go.mod": "module example.test/root\n\ngo 1.26.0\n"}}, BaseExecution: verification.ExecutionRequest{Executable: "go", SourceDirectory: "/trusted/repo", Environment: v.Environment(), Limits: verification.ExecutionLimits{Timeout: time.Minute, StdoutBytes: 1024 * 1024, StderrBytes: 1024 * 1024, InputBytes: 1024 * 1024, InputEntries: 100, WorkspaceBytes: 64 * 1024 * 1024, TempBytes: 16 * 1024 * 1024, CPUMillis: 1000, MemoryBytes: 512 * 1024 * 1024, PIDs: 128}}}
	return v, r
}
func TestPlansAllGoCapabilitiesAndPinsPolicy(t *testing.T) {
	v, r := fixture(t)
	for cap, flag := range map[verification.Capability]string{Build: "build", Test: "-count=1", Race: "-race", Vet: "vet", Staticcheck: "-f=json", Vulncheck: "-db=file:///trusted/vulndb", Fuzz: "-fuzz=^FuzzExample$", Benchmark: "-bench=."} {
		r.Capability = cap
		p, err := v.Plan(t.Context(), r)
		if err != nil || len(p.Steps) != 1 || !slices.Contains(p.Steps[0].Request.Arguments, flag) {
			t.Fatal(cap, p, err)
		}
		if p.Steps[0].Request.SourceDirectory != r.BaseExecution.SourceDirectory || p.Steps[0].Request.Limits != r.BaseExecution.Limits || !maps.Equal(p.Steps[0].Request.Environment, r.BaseExecution.Environment) {
			t.Fatal("policy changed")
		}
	}
	r.BaseExecution.Environment["GOFLAGS"] = "-toolexec=malicious"
	if _, err := v.Plan(t.Context(), r); err == nil {
		t.Fatal("untrusted flags accepted")
	}
}
func TestWorkspaceAndMultiModuleDiscovery(t *testing.T) {
	v, r := fixture(t)
	r.Target.Files = []string{"go.work", "a/go.mod", "b/go.mod"}
	r.Target.Metadata = map[string]string{"go.work": "go 1.26.0\nuse (\n ./a\n ./b\n)\n", "a/go.mod": "module example.test/a\ngo 1.26.0\n", "b/go.mod": "module example.test/b\ngo 1.26.0\n"}
	for _, workspace := range []bool{true, false} {
		if !workspace {
			r.Target.Files = r.Target.Files[1:]
			delete(r.Target.Metadata, "go.work")
		}
		p, err := v.Plan(t.Context(), r)
		if err != nil || len(p.Steps) != 2 || p.Steps[0].Request.WorkingDirectory != "a" || p.Steps[1].Request.WorkingDirectory != "b" {
			t.Fatal(p, err)
		}
	}
}
func TestManifestAndBudgetSecurity(t *testing.T) {
	for _, attack := range []string{"work traversal", "work absolute", "replace escape", "missing text", "unknown module", "omitted module", "syntax", "fuzz injection", "fuzz budget", "network DB"} {
		t.Run(attack, func(t *testing.T) {
			v, r := fixture(t)
			switch attack {
			case "work traversal", "work absolute", "unknown module", "omitted module":
				r.Target.Files = append(r.Target.Files, "go.work")
				use := "../outside"
				if attack == "work absolute" {
					use = "/etc"
				}
				if attack == "unknown module" {
					use = "./missing"
				}
				if attack == "omitted module" {
					use = "./other"
					r.Target.Files = append(r.Target.Files, "other/go.mod")
					r.Target.Metadata["other/go.mod"] = "module other\ngo 1.26.0\n"
				}
				r.Target.Metadata["go.work"] = "go 1.26.0\nuse " + use + "\n"
			case "replace escape":
				r.Target.Metadata["go.mod"] += "replace example.test/dep => ../outside\n"
			case "missing text":
				delete(r.Target.Metadata, "go.mod")
			case "syntax":
				r.Target.Metadata["go.mod"] = "malformed ("
			case "fuzz injection":
				c := v.config
				c.FuzzTargets = []FuzzTarget{{Module: ".", Package: "../outside", Function: "Fuzz;evil"}}
				if _, err := New(c); err == nil {
					t.Fatal("fuzz injection accepted")
				}
				return
			case "fuzz budget":
				r.Capability = Fuzz
				r.BaseExecution.Limits.Timeout = time.Second
			case "network DB":
				c := v.config
				c.VulnerabilityDB = "http://169.254.169.254/metadata"
				if _, err := New(c); err == nil {
					t.Fatal("network DB accepted")
				}
				return
			}
			if _, err := v.Plan(t.Context(), r); err == nil {
				t.Fatal("unsafe plan", attack)
			}
		})
	}
}
func events(name string) string {
	return `{"Action":"start","Package":"example.test/root"}` + "\n" + `{"Action":"run","Package":"example.test/root","Test":"` + name + `"}` + "\n" + `{"Action":"pass","Package":"example.test/root","Test":"` + name + `"}` + "\n" + `{"Action":"pass","Package":"example.test/root"}`
}
func TestNormalizedGoEvidenceAndPoisoning(t *testing.T) {
	v, _ := fixture(t)
	config := `{"config":{"protocol_version":"v1.0.0","scanner_name":"govulncheck","scan_level":"symbol","db":"file:///trusted/vulndb"}}` + "\n" + `{"SBOM":{"roots":["example.test/root"]}}`
	for _, tc := range []struct {
		cap            verification.Capability
		stdout, stderr string
		exit           int
		want           evidence.Status
	}{
		{Build, "", "", 0, evidence.StatusPass}, {Build, "", "compiler failure", 1, evidence.StatusFail},
		{Test, events("TestExample"), "", 0, evidence.StatusPass}, {Race, events("TestExample"), "", 0, evidence.StatusPass}, {Fuzz, events("FuzzExample"), "", 0, evidence.StatusPass},
		{Test, `{"Action":"start","Package":"p"}`, "", 0, evidence.StatusUnknown},
		{Test, `{"Action":"start","Package":"p"}` + "\n" + `{"Action":"skip","Package":"p"}`, "", 0, evidence.StatusSkipped},
		{Vet, "", `# p` + "\n" + `{"p":{"printf":[{"posn":"main.go:2","message":"bad format"}]}}`, 1, evidence.StatusFail},
		{Staticcheck, `{"code":"SA1000","severity":"error","message":"problem","location":{"file":"main.go","line":2,"column":1}}`, "", 1, evidence.StatusFail},
		{Staticcheck, "", "", 0, evidence.StatusPass},
		{Vulncheck, config, "", 0, evidence.StatusPass},
		{Vulncheck, config + "\n" + `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"stdlib","function":"Danger"}]}}`, "", 0, evidence.StatusFail},
		{Vulncheck, config + "\n" + `{"finding":{"osv":"GO-2026-0001","trace":[{"module":"stdlib"}]}}`, "", 0, evidence.StatusWarn},
	} {
		r := verification.ExecutionResult{Status: verification.Completed, ExitCode: tc.exit, Stdout: []byte(tc.stdout), Stderr: []byte(tc.stderr)}
		if tc.exit != 0 {
			r.Status = verification.Failed
		}
		report, err := v.normalize(tc.cap, r)
		if err != nil || report.Status != tc.want {
			t.Fatal(tc.cap, report, err)
		}
	}
	for _, raw := range []string{`{"Action":"start","Action":"pass","Package":"p"}`, `{"Action":"start","ACTION":"pass","Package":"p"}`, `{"Action":"pass","Package":"p"}`, `{"Action":"UNKNOWN","Package":"p"}`, `{"Action":"start","Package":"p"} garbage`} {
		if report, err := v.normalize(Test, verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Stdout: []byte(raw)}); err == nil && report.Status == evidence.StatusPass {
			t.Fatal("poisoned PASS", raw)
		}
	}
	if report, err := v.normalize(Vulncheck, verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Stdout: []byte(strings.Split(config, "\n")[0])}); err == nil && report.Status == evidence.StatusPass {
		t.Fatal("config-only PASS")
	}
}
func TestOptionalToolsUnavailableAndCancellation(t *testing.T) {
	v, r := fixture(t)
	v.config.StaticcheckTool = ""
	registry, _ := verification.NewRegistry(v)
	r.Capability = Staticcheck
	record, err := registry.Run(t.Context(), ID, r, noRun{})
	if !errors.Is(err, verification.ErrVerifierUnavailable) || record.Report.Status != evidence.StatusUnavailable {
		t.Fatal(record, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.Plan(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBenchmarkFragmentsRequireActualBenchmarkRun(t *testing.T) {
	v, _ := fixture(t)
	start := `{"Action":"start","Package":"p"}` + "\n"
	finish := `{"Action":"pass","Package":"p"}`
	run := `{"Action":"run","Package":"p","Test":"BenchmarkEcho"}` + "\n"
	output := `{"Action":"output","Package":"p","Test":"BenchmarkEcho","Output":"BenchmarkEcho-2  "}` + "\n" + `{"Action":"output","Package":"p","Test":"BenchmarkEcho","Output":"1  100 ns/op  0 B/op  0 allocs/op\n"}` + "\n"
	r := verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Stdout: []byte(start + run + output + finish)}
	report, err := v.normalize(Benchmark, r)
	if err != nil || report.Status != evidence.StatusPass || len(report.Findings) != 1 {
		t.Fatal(report, err)
	}
	r.Stdout = []byte(start + output + finish)
	if report, err := v.normalize(Benchmark, r); err == nil && report.Status == evidence.StatusPass {
		t.Fatal("output forged benchmark success")
	}
}

type noRun struct{}

func (noRun) Run(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
	panic("unexpected execution")
}
