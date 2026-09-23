package scanners

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/platform/evidencestore"
	"safepatch/internal/platform/runner"
	"safepatch/internal/verification"
)

// This fixture process exercises real Runner isolation/capture/exit handling.
// It is intentionally not represented as a live upstream scanner test.
func TestScannerFixtureProcess(t *testing.T) {
	i := slices.Index(os.Args, "--scanner-fixture")
	if i < 0 {
		return
	}
	tool := Tool(os.Args[i+1])
	if !slices.Contains(os.Args[i+2:], "--config") {
		os.Exit(2)
	}
	fmt.Fprint(os.Stdout, clean[tool])
	os.Exit(0)
}

func TestScannersThroughLocalRunnerAndArchive(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			a, input := fixture(t, tool)
			source := t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("original source"), 0600); err != nil {
				t.Fatal(err)
			}
			input.BaseExecution.SourceDirectory = source
			input.BaseExecution.Mode = verification.TrustedLocal
			l := &input.BaseExecution.Limits
			l.CPUMillis = 0
			l.MemoryBytes = 0
			l.PIDs = 0
			l.WorkspaceBytes = 0
			l.TempBytes = 0
			var env []string
			for key := range a.Environment() {
				env = append(env, key)
			}
			local, err := runner.NewLocalRunner(runner.Config{TemporaryDirectory: t.TempDir(), Tools: map[string]string{string(tool): executable}, EnvironmentAllowlist: env})
			if err != nil {
				t.Fatal(err)
			}
			fixtureRunner := resultRunner(func(ctx context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
				r.Arguments = append([]string{"-test.run=^TestScannerFixtureProcess$", "--", "--scanner-fixture", string(tool)}, r.Arguments...)
				return local.Run(ctx, r)
			})
			scope := evidence.ArchiveScope{OrganizationID: "local-cli", Subject: input.Target.Subject}
			store, err := evidencestore.NewLocal(t.TempDir(), scope)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			// File and directory flushes can exceed one second while release
			// builds contend for disk. Deadline/failure behavior has separate tests.
			e, err := NewExecutor(a, fixtureRunner, store, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Run(t.Context(), input, scope)
			if err != nil || result.Record.Report.Status != evidence.StatusPass || result.Manifest == nil || result.Record.Executions[0].Sandboxed {
				t.Fatal(result, err)
			}
			original, err := store.Get(t.Context(), scope, result.Originals[0].Stdout)
			if err != nil || string(original) != clean[tool] {
				t.Fatal("capture changed", err)
			}
			actual, err := os.ReadFile(filepath.Join(source, "main.go"))
			if err != nil || string(actual) != "original source" {
				t.Fatal("source changed", err)
			}
		})
	}
}
