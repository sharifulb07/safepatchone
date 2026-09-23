package scanners

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/platform/runner"
	"safepatch/internal/verification"
)

// Opt-in smoke test for administrator-installed engines, rules and offline DBs.
// It never downloads tools/data or scans the developer's actual repository.
func TestInstalledScanners(t *testing.T) {
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			prefix := "SAFEPATCH_" + strings.ToUpper(string(tool)) + "_"
			executable := os.Getenv(prefix + "EXECUTABLE")
			if executable == "" {
				t.Skip("installed scanner not configured: " + prefix + "EXECUTABLE")
			}
			config := Config{Executable: string(tool), Version: os.Getenv(prefix + "VERSION"), ConfigFile: os.Getenv(prefix + "CONFIG"), IgnoreFile: os.Getenv(prefix + "IGNORE"), DatabaseDirectory: os.Getenv(prefix + "DATABASE")}
			a, err := New(tool, config)
			if err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			for name, content := range map[string]string{"main.go": "package main\nfunc main() {}\n", "go.mod": "module example.test/scannerfixture\n\ngo 1.26.0\n", "requirements.txt": "requests==2.31.0\n"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var env []string
			for key := range a.Environment() {
				env = append(env, key)
			}
			var search []string
			if p := os.Getenv("SAFEPATCH_SCANNER_TOOL_PATH"); p != "" {
				search = filepath.SplitList(p)
			}
			local, err := runner.NewLocalRunner(runner.Config{TemporaryDirectory: t.TempDir(), Tools: map[string]string{string(tool): executable}, EnvironmentAllowlist: env, TrustedSearchPath: search})
			if err != nil {
				t.Fatal(err)
			}
			subject := evidence.Subject{RepositoryID: "scanner-fixture", Revision: "fixture-v1"}
			scope := evidence.ArchiveScope{OrganizationID: "local-cli", Subject: subject}
			store, err := evidencestore.NewLocal(t.TempDir(), scope)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			e, err := NewExecutor(a, local, store, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			input := verification.PlanRequest{Target: verification.Target{Subject: subject, Files: []string{"main.go", "go.mod", "requirements.txt"}}, Capability: a.Capabilities()[0], BaseExecution: verification.ExecutionRequest{Executable: string(tool), SourceDirectory: source, Mode: verification.TrustedLocal, Environment: a.Environment(), Limits: verification.ExecutionLimits{Timeout: 2 * time.Minute, StdoutBytes: evidence.MaxOriginalBytes, StderrBytes: 1024 * 1024, InputBytes: 1024 * 1024, InputEntries: 100}}}
			result, err := e.Run(t.Context(), input, scope)
			if err != nil || (result.Record.Report.Status != evidence.StatusPass && result.Record.Report.Status != evidence.StatusFail) || len(result.Originals) != 1 || result.Manifest == nil {
				t.Fatalf("installed scanner did not complete: status=%s err=%v", result.Record.Report.Status, err)
			}
			if raw, err := store.Get(t.Context(), scope, result.Originals[0].Stdout); err != nil || evidence.Digest(raw) != result.Record.Executions[0].StdoutSHA256 {
				t.Fatal("original evidence mismatch", err)
			}
		})
	}
}
