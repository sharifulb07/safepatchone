package golang

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"safepatch/internal/evidence"
	platformrunner "safepatch/internal/platform/runner"
	"safepatch/internal/verification"
)

// Explicit opt-in because compiling the standard library/race runtime with a
// fresh private cache is expensive. This runs real Go tools, never a mock.
func TestNativeGoVerification(t *testing.T) {
	if os.Getenv("SAFEPATCH_GO_LIVE") != "1" {
		t.Skip("set SAFEPATCH_GO_LIVE=1 for real Go build/test/race/vet/fuzz/benchmark checks")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	search := []string{filepath.Dir(goTool)}
	compiler, compilerErr := exec.LookPath("gcc")
	if compilerErr == nil {
		search = append(search, filepath.Dir(compiler))
	}
	config := Config{GoTool: "go", Parallelism: 2, CGOEnabled: compilerErr == nil, FuzzSeconds: 1, FuzzTargets: []FuzzTarget{{Module: ".", Package: ".", Function: "FuzzEcho"}}, BenchmarkIterations: 1, BuildCache: t.TempDir()}
	tools := map[string]string{"go": goTool}
	if tool := os.Getenv("SAFEPATCH_STATICCHECK"); tool != "" {
		tools["staticcheck"] = tool
		config.StaticcheckTool = "staticcheck"
	}
	if tool := os.Getenv("SAFEPATCH_GOVULNCHECK"); tool != "" {
		tools["govulncheck"] = tool
		config.GovulncheckTool = "govulncheck"
		config.VulnerabilityDB = os.Getenv("SAFEPATCH_VULN_DB")
	}
	v, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	source, base := t.TempDir(), t.TempDir()
	files := map[string]string{"go.mod": "module example.test/fixture\ngo 1.26.0\n", "main.go": "package fixture\nfunc Echo(s string) string { return s }\n", "main_test.go": "package fixture\nimport \"testing\"\nfunc TestEcho(t *testing.T){if Echo(\"ok\")!=\"ok\"{t.Fatal(\"bad\")}}\nfunc FuzzEcho(f *testing.F){f.Add(\"ok\");f.Fuzz(func(t *testing.T,s string){if Echo(s)!=s{t.Fatal(\"bad\")}})}\nfunc BenchmarkEcho(b *testing.B){for i:=0;i<b.N;i++{Echo(\"ok\")}}\n"}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := v.Environment()
	allow := []string{}
	for key := range env {
		allow = append(allow, key)
	}
	r, err := platformrunner.NewLocalRunner(platformrunner.Config{TemporaryDirectory: base, Tools: tools, EnvironmentAllowlist: allow, TrustedSearchPath: search})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := verification.NewRegistry(v)
	if err != nil {
		t.Fatal(err)
	}
	input := verification.PlanRequest{Target: verification.Target{Subject: evidence.Subject{RepositoryID: "fixture", Revision: "fixture-v1"}, Files: []string{"go.mod", "main.go", "main_test.go"}, Metadata: map[string]string{"go.mod": files["go.mod"]}}, BaseExecution: verification.ExecutionRequest{Executable: "go", SourceDirectory: source, Environment: env, Mode: verification.TrustedLocal, Limits: verification.ExecutionLimits{Timeout: 3 * time.Minute, StdoutBytes: 1024 * 1024, StderrBytes: 1024 * 1024, InputBytes: 1024 * 1024, InputEntries: 100}}}
	for _, cap := range []verification.Capability{Build, Test, Race, Vet, Fuzz, Benchmark, Staticcheck, Vulncheck} {
		t.Run(string(cap), func(t *testing.T) {
			if cap == Staticcheck && config.StaticcheckTool == "" {
				t.Skip("SAFEPATCH_STATICCHECK not configured")
			}
			if cap == Vulncheck && (config.GovulncheckTool == "" || config.VulnerabilityDB == "") {
				t.Skip("SAFEPATCH_GOVULNCHECK and SAFEPATCH_VULN_DB not configured")
			}
			if cap == Race && compilerErr != nil {
				t.Skip("C compiler unavailable")
			}
			input.Capability = cap
			var outputs []verification.ExecutionResult
			record, err := registry.Run(t.Context(), ID, input, captureRunner{r, &outputs})
			if err != nil || record.Report.Status != evidence.StatusPass {
				for _, out := range outputs {
					t.Logf("stdout=%s\nstderr=%s", out.Stdout, out.Stderr)
				}
				t.Fatal(record, err)
			}
			if _, err := record.CanonicalJSON(); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(base)
			if err != nil || len(entries) != 0 {
				t.Fatal("workspace cleanup", entries, err)
			}
		})
	}
	// A failing test must remain FAIL after real process execution/normalization.
	failing := files["main_test.go"] + "\nfunc TestFailure(t *testing.T){t.Fatal(\"expected fixture failure\")}\n"
	if err := os.WriteFile(filepath.Join(source, "main_test.go"), []byte(failing), 0600); err != nil {
		t.Fatal(err)
	}
	input.Capability = Test
	record, err := registry.Run(t.Context(), ID, input, r)
	if err != nil || record.Report.Status != evidence.StatusFail {
		t.Fatal("failing test became successful", record, err)
	}
	if err := os.WriteFile(filepath.Join(source, "main_test.go"), []byte(files["main_test.go"]), 0600); err != nil {
		t.Fatal(err)
	}
	for name, original := range files {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil || string(data) != original {
			t.Fatal("source modified", name, err)
		}
	}
}

func TestNativeGoModules(t *testing.T) {
	if os.Getenv("SAFEPATCH_GO_LIVE") != "1" {
		t.Skip("set SAFEPATCH_GO_LIVE=1 for real workspace and multi-module verification")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(Config{GoTool: "go", Parallelism: 2})
	if err != nil {
		t.Fatal(err)
	}
	source, base := t.TempDir(), t.TempDir()
	for _, dir := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(source, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	metadata := map[string]string{"go.work": "go 1.26.0\nuse (\n ./a\n ./b\n)\n", "a/go.mod": "module example.test/a\ngo 1.26.0\nrequire example.test/b v0.0.0\n", "b/go.mod": "module example.test/b\ngo 1.26.0\n"}
	for name, data := range metadata {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "a", "a.go"), []byte("package a\nimport \"example.test/b\"\nfunc A()int{return b.B()}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "b", "b.go"), []byte("package b\nfunc B()int{return 1}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := v.Environment()
	allow := []string{}
	for key := range env {
		allow = append(allow, key)
	}
	r, err := platformrunner.NewLocalRunner(platformrunner.Config{TemporaryDirectory: base, Tools: map[string]string{"go": goTool}, EnvironmentAllowlist: allow, TrustedSearchPath: []string{filepath.Dir(goTool)}})
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := verification.NewRegistry(v)
	input := verification.PlanRequest{Capability: Build, Target: verification.Target{Subject: evidence.Subject{RepositoryID: "fixture", Revision: "workspace"}, Files: []string{"go.work", "a/go.mod", "b/go.mod", "a/a.go", "b/b.go"}, Metadata: metadata}, BaseExecution: verification.ExecutionRequest{Executable: "go", SourceDirectory: source, Environment: env, Mode: verification.TrustedLocal, Limits: verification.ExecutionLimits{Timeout: time.Minute, StdoutBytes: 1024 * 1024, StderrBytes: 1024 * 1024, InputBytes: 1024 * 1024, InputEntries: 100}}}
	for _, mode := range []string{"workspace", "independent modules"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "independent modules" {
				if err := os.Remove(filepath.Join(source, "go.work")); err != nil {
					t.Fatal(err)
				}
				delete(metadata, "go.work")
				input.Target.Files = input.Target.Files[1:]
				metadata["a/go.mod"] = "module example.test/a\ngo 1.26.0\n"
				if err := os.WriteFile(filepath.Join(source, "a", "go.mod"), []byte(metadata["a/go.mod"]), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "a", "a.go"), []byte("package a\nfunc A()int{return 1}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var outputs []verification.ExecutionResult
			record, err := registry.Run(t.Context(), ID, input, captureRunner{r, &outputs})
			if err != nil || record.Report.Status != evidence.StatusPass || len(record.Executions) != 2 {
				for _, out := range outputs {
					t.Logf("%s %s", out.Stdout, out.Stderr)
				}
				t.Fatal(record, err)
			}
		})
	}
}

type captureRunner struct {
	verification.Runner
	outputs *[]verification.ExecutionResult
}

func (r captureRunner) Run(ctx context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	result, err := r.Runner.Run(ctx, request)
	*r.outputs = append(*r.outputs, result)
	return result, err
}
