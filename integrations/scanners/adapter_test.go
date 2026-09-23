package scanners

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

var versions = map[Tool]string{Gitleaks: "8.24.2", Semgrep: "1.130.0", Trivy: "0.69.0", OSV: "2.3.3", Syft: "1.33.0"}
var clean = map[Tool]string{
	Gitleaks: "[ ]\n",
	Semgrep:  `{"version":"1.130.0","results":[],"errors":[],"paths":{"scanned":["main.go"]}}`,
	Trivy:    `{"SchemaVersion":2,"ArtifactType":"filesystem","Results":[{"Target":"go.mod","Packages":[{"Name":"example","Version":"1.0.0"}]}]}`,
	OSV:      `{"results":[{"source":{"path":"go.mod","type":"lockfile"},"packages":[{"package":{"name":"example","version":"1.0.0","ecosystem":"Go"}}]}]}`,
	Syft:     `{"schema":{"version":"16.0.0"},"descriptor":{"name":"syft","version":"1.33.0"},"source":{"type":"directory"},"artifacts":[{"id":"package-id","name":"example","type":"go-module","version":"1.0.0"}]}`,
}
var findings = map[Tool]string{
	Gitleaks: `[{"RuleID":"generic-secret","File":"main.go","StartLine":1,"Secret":"fixture-secret-never-log","Match":"password = fixture-secret-never-log"}]`,
	Semgrep:  `{"version":"1.130.0","results":[{"check_id":"test.rule","path":"main.go","start":{"line":1},"extra":{"severity":"WARNING","message":"fixture-secret-never-log","lines":"fixture-secret-never-log"}}],"errors":[],"paths":{"scanned":["main.go"]}}`,
	Trivy:    `{"SchemaVersion":2,"ArtifactType":"filesystem","Results":[{"Target":"go.mod","Vulnerabilities":[{"VulnerabilityID":"CVE-2026-1234","PkgName":"example","Description":"fixture-secret-never-log"}]}]}`,
	OSV:      `{"results":[{"source":{"path":"go.mod","type":"lockfile"},"packages":[{"package":{"name":"example"},"vulnerabilities":[{"id":"GHSA-aaaa-bbbb-cccc","details":"fixture-secret-never-log"}]}]}]}`,
}

func fixture(t *testing.T, tool Tool) (Adapter, verification.PlanRequest) {
	t.Helper()
	a, err := New(tool, Config{Executable: string(tool), Version: versions[tool], ConfigFile: "/trusted/config", IgnoreFile: "/trusted/empty", DatabaseDirectory: "/trusted/db"})
	if err != nil {
		t.Fatal(err)
	}
	request := verification.ExecutionRequest{Executable: string(tool), SourceDirectory: "/source", Mode: verification.Sandbox, Environment: a.Environment(), Limits: verification.ExecutionLimits{Timeout: time.Minute, StdoutBytes: 1024 * 1024, StderrBytes: 1024 * 1024, InputBytes: 1024 * 1024, InputEntries: 1000, CPUMillis: 1000, MemoryBytes: 512 * 1024 * 1024, PIDs: 64, WorkspaceBytes: 2 * 1024 * 1024, TempBytes: 1024 * 1024}}
	return a, verification.PlanRequest{Target: verification.Target{Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}, Files: []string{"main.go", "go.mod"}}, Capability: a.Capabilities()[0], BaseExecution: request}
}

type resultRunner func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error)

func (f resultRunner) Run(c context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
	return f(c, r)
}
func output(raw string, code int) resultRunner {
	return func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
		r := verification.ExecutionResult{Status: verification.Completed, ExitCode: code, Sandboxed: true, Stdout: []byte(raw)}
		if code != 0 {
			r.Status = verification.Failed
			return r, verification.ErrExecutionFailed
		}
		return r, nil
	}
}

type memoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
	fail bool
}

func (s *memoryStore) Put(ctx context.Context, scope evidence.ArchiveScope, raw []byte) (evidence.OriginalReference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail || ctx.Err() != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	ref := evidence.OriginalReference{SHA256: evidence.Digest(raw), Bytes: int64(len(raw))}
	s.data[scope.OrganizationID+ref.SHA256] = slices.Clone(raw)
	return ref, nil
}
func (s *memoryStore) Get(ctx context.Context, scope evidence.ArchiveScope, ref evidence.OriginalReference) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[scope.OrganizationID+ref.SHA256]
	if !ok {
		return nil, evidence.ErrOriginalEvidence
	}
	return slices.Clone(b), nil
}

func TestAdaptersPlanAndNormalize(t *testing.T) {
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			a, input := fixture(t, tool)
			plan, err := a.Plan(t.Context(), input)
			if err != nil || len(plan.Steps) != 1 {
				t.Fatal(plan, err)
			}
			r := plan.Steps[0].Request
			if r.SourceDirectory != input.BaseExecution.SourceDirectory || r.Mode != input.BaseExecution.Mode || r.Limits != input.BaseExecution.Limits || !maps.Equal(r.Environment, input.BaseExecution.Environment) || r.WorkingDirectory != "." {
				t.Fatal("changed trusted policy")
			}
			if !slices.Contains(r.Arguments, "--config") || !slices.Contains(r.Arguments, "/trusted/config") {
				t.Fatal("implicit repository config")
			}
			for _, arg := range r.Arguments {
				if strings.Contains(arg, "http") || arg == "--download-offline-databases" || arg == "--autofix" {
					t.Fatal("unsafe command", arg)
				}
			}
			registry, _ := verification.NewRegistry(a)
			record, err := registry.Run(t.Context(), a.ID(), input, output(clean[tool], 0))
			if err != nil || record.Report.Status != evidence.StatusPass {
				t.Fatal(record, err)
			}
			if raw, ok := findings[tool]; ok {
				record, err = registry.Run(t.Context(), a.ID(), input, output(raw, 1))
				if err != nil || record.Report.Status != evidence.StatusFail || len(record.Report.Findings) == 0 {
					t.Fatal(record, err)
				}
				encoded, err := record.CanonicalJSON()
				if err != nil || bytes.Contains(encoded, []byte("fixture-secret-never-log")) {
					t.Fatal("secret leaked", err)
				}
			}
		})
	}
}

func TestOriginalsSurviveNormalizationAndErrors(t *testing.T) {
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			a, input := fixture(t, tool)
			scope := evidence.ArchiveScope{OrganizationID: "org_a", Subject: input.Target.Subject}
			for _, raw := range []string{clean[tool], "{broken json\nfixture-secret-never-log", strings.Repeat(" ", 20) + clean[tool] + "\n"} {
				store := &memoryStore{}
				executor, _ := NewExecutor(a, output(raw, 0), store, time.Second)
				result, err := executor.Run(t.Context(), input, scope)
				if strings.HasPrefix(raw, "{broken") {
					if err == nil || result.Record.Report.Status == evidence.StatusPass {
						t.Fatal("malformed passed")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if len(result.Originals) != 1 || result.Manifest == nil {
					t.Fatal("missing evidence", result)
				}
				original, err := store.Get(t.Context(), scope, result.Originals[0].Stdout)
				if err != nil || string(original) != raw {
					t.Fatal("original changed", err)
				}
				if result.Record.Executions[0].StdoutSHA256 != result.Originals[0].Stdout.SHA256 {
					t.Fatal("digest mismatch")
				}
			}
		})
	}
}

func TestFailClosedProtocols(t *testing.T) {
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		t.Run(string(tool), func(t *testing.T) {
			a, input := fixture(t, tool)
			registry, _ := verification.NewRegistry(a)
			for _, raw := range []string{"", "null", "{}", `{"results":[],"results":[{}]}`, `{"results":[],"RESULTS":[{}]}`, clean[tool] + " {}", `{"x":"\ud800"}`, `{"x":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}"} {
				record, err := registry.Run(t.Context(), a.ID(), input, output(raw, 0))
				if err == nil || record.Report.Status == evidence.StatusPass {
					t.Fatal("ambiguous output admitted", raw, record, err)
				}
			}
			for _, code := range []int{1, 2, 127} {
				record, err := registry.Run(t.Context(), a.ID(), input, output(clean[tool], code))
				if record.Report.Status == evidence.StatusPass || (code == 1 && tool != Syft && err == nil) {
					t.Fatal("bad exit accepted", code, record, err)
				}
			}
			runner := func(status verification.ExecutionStatus, err error, truncated bool) resultRunner {
				return func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
					return verification.ExecutionResult{Status: status, ExitCode: -1, Sandboxed: true, Stdout: []byte(clean[tool]), StdoutTruncated: truncated}, err
				}
			}
			for _, r := range []resultRunner{runner(verification.TimedOut, context.DeadlineExceeded, false), runner(verification.Cancelled, context.Canceled, false), runner(verification.OutputLimited, verification.ErrOutputLimit, true), runner(verification.Unknown, nil, false)} {
				record, _ := registry.Run(t.Context(), a.ID(), input, r)
				if record.Report.Status == evidence.StatusPass {
					t.Fatal("incomplete passed")
				}
			}
			warn := resultRunner(func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
				return verification.ExecutionResult{Status: verification.Completed, Sandboxed: true, Stdout: []byte(clean[tool]), Stderr: []byte("partial scan")}, nil
			})
			record, err := registry.Run(t.Context(), a.ID(), input, warn)
			if err != nil || record.Report.Status != evidence.StatusUnknown {
				t.Fatal(record, err)
			}
		})
	}
}

func TestConfigurationAndCancellation(t *testing.T) {
	for _, p := range []string{"../rules", "https://rules.test/a", "/trusted/../rules", "/workspace/rules", "/input/db", `C:\source\..\config`, "//host/share/config"} {
		if _, err := New(Gitleaks, Config{Version: "8.24.2", ConfigFile: p}); err == nil {
			t.Fatal("unsafe config", p)
		}
	}
	for _, tool := range []Tool{Gitleaks, Semgrep, Trivy, OSV, Syft} {
		a, input := fixture(t, tool)
		input.BaseExecution.Environment["SCANNER_TOKEN"] = "secret"
		if _, err := a.Plan(t.Context(), input); err == nil {
			t.Fatal("unapproved environment")
		}
		input.BaseExecution.Environment = a.Environment()
		input.BaseExecution.SourceDirectory = "/trusted"
		if _, err := a.Plan(t.Context(), input); err == nil {
			t.Fatal("repository policy accepted")
		}
		missing, _ := New(tool, Config{Version: versions[tool]})
		input.BaseExecution.SourceDirectory = "/source"
		input.BaseExecution.Environment = missing.Environment()
		if _, err := missing.Plan(t.Context(), input); !errors.Is(err, verification.ErrVerifierUnavailable) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := a.Detect(ctx, input.Target); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	a, input := fixture(t, Semgrep)
	input.Target.Files = append(input.Target.Files, "nested/.semgrepignore")
	if _, err := a.Plan(t.Context(), input); err == nil {
		t.Fatal("untrusted suppressions")
	}
}

func TestArchiveFailureAndCancelledCapture(t *testing.T) {
	a, input := fixture(t, Gitleaks)
	scope := evidence.ArchiveScope{OrganizationID: "org_a", Subject: input.Target.Subject}
	store := &memoryStore{fail: true}
	e, _ := NewExecutor(a, output(clean[Gitleaks], 0), store, time.Second)
	r, err := e.Run(t.Context(), input, scope)
	if !errors.Is(err, evidence.ErrOriginalEvidence) || r.Record.Report.Status != evidence.StatusError {
		t.Fatal(r, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	store = &memoryStore{}
	e, _ = NewExecutor(a, resultRunner(func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error) {
		cancel()
		return verification.ExecutionResult{Status: verification.Cancelled, ExitCode: -1, Stdout: []byte("partial\x00original"), Sandboxed: true}, context.Canceled
	}), store, time.Second)
	r, err = e.Run(ctx, input, scope)
	if err == nil || r.Record.Report.Status == evidence.StatusPass || len(r.Originals) != 1 || r.Manifest == nil {
		t.Fatal(r, err)
	}
	raw, _ := store.Get(t.Context(), scope, r.Originals[0].Stdout)
	if string(raw) != "partial\x00original" {
		t.Fatal("lost cancelled output")
	}
	scope.Subject.Revision = "other"
	if _, err = e.Run(t.Context(), input, scope); err == nil {
		t.Fatal("subject mismatch")
	}
}
