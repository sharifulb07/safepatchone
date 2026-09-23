package languages_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/sir"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages"
	"safepatch/languages/golang"
)

func registry(t *testing.T) *languagepack.Registry {
	t.Helper()
	r, err := languages.NewRegistry(golang.Config{GoTool: "go", Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func input(language, file string) sir.Extraction {
	return sir.Extraction{Metadata: sir.Metadata{Schema: sir.Schema, TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}, Producer: "parser", ProducerVersion: "1.0.0", Deterministic: true, Coverage: sir.CoveragePartial}, Files: []sir.SourceFile{{Path: file, ContentHash: evidence.Digest(nil)}}, Concepts: []sir.Concept{{ID: "node", Language: language, NativeKind: "function", Name: "main", Source: evidence.SourceLocation{Path: file}}}}
}

func TestInstalledPacksHaveHonestCapabilities(t *testing.T) {
	r := registry(t)
	d := r.Discover()
	ids := []string{}
	for _, item := range d {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, []string{"csharp", "go", "java", "javascript", "kotlin", "php", "python", "ruby", "rust", "typescript"}) {
		t.Fatal(ids)
	}
	files := map[string]string{"go": "cmd/main.go", "javascript": "web/main.jsx", "typescript": "web/main.tsx", "python": "worker/main.py", "java": "src/Main.java", "rust": "src/main.rs", "csharp": "src/Main.cs", "kotlin": "src/Main.kt", "ruby": "lib/main.rb", "php": "src/main.php"}
	for _, descriptor := range d {
		t.Run(descriptor.ID, func(t *testing.T) {
			file := files[descriptor.ID]
			in := input(descriptor.ID, file)
			detected, err := r.Detect(t.Context(), descriptor.ID, verification.Target{Subject: in.Subject, Files: []string{file, "package.json"}, Metadata: map[string]string{"package.json": `{"scripts":{"install":"send secrets"}}`}})
			if err != nil || detected.State != verification.Detected || !slices.Equal(detected.Files, []string{file}) {
				t.Fatal(detected, err)
			}
			for _, kind := range sir.Kinds() {
				in.Concepts[0].NativeKind = string(kind)
				in.Concepts[0].Action = ""
				switch kind {
				case sir.DBOperation, sir.NetworkOperation, sir.FilesystemOperation, sir.ProcessOperation, sir.ConcurrencyOperation, sir.SideEffect:
					in.Concepts[0].Action = "observe"
				}
				out, err := r.Normalize(t.Context(), descriptor.ID, in)
				if err != nil {
					t.Fatal(kind, err)
				}
				fields, _ := out.Fields()
				if fields.Nodes[0].Kind != kind || fields.Coverage != sir.CoveragePartial {
					t.Fatal(fields)
				}
			}
			vr, err := r.VerifierRegistry(descriptor.ID)
			if descriptor.ID == "go" {
				if err != nil || len(vr.Discover(golang.Build)) != 1 {
					t.Fatal(err)
				}
			} else if !errors.Is(err, languagepack.ErrUnavailable) || slices.Contains(descriptor.Capabilities, languagepack.Verify) {
				t.Fatal("unimplemented verification advertised", err)
			}
		})
	}
	if _, err := languages.NewRegistry(golang.Config{}); err == nil {
		t.Fatal("invalid trusted Go configuration")
	}
}

type runnerFunc func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error)

func (f runnerFunc) Run(ctx context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	return f(ctx, request)
}

func TestGoPackReusesExistingVerifierAndRunnerLifecycle(t *testing.T) {
	r := registry(t)
	vr, err := r.VerifierRegistry("go")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := golang.New(golang.Config{GoTool: "go", Parallelism: 1})
	target := verification.Target{Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}, Files: []string{"go.mod", "main.go"}, Metadata: map[string]string{"go.mod": "module example.test/app\ngo 1.26.0\n"}}
	base := verification.ExecutionRequest{Executable: "go", SourceDirectory: "/trusted/repo", Environment: v.Environment(), Limits: verification.ExecutionLimits{Timeout: time.Second, StdoutBytes: 1024, StderrBytes: 1024, InputBytes: 4096, InputEntries: 10, WorkspaceBytes: 8192, TempBytes: 4096, CPUMillis: 1000, MemoryBytes: 1024 * 1024, PIDs: 10}}
	calls := 0
	_, err = vr.Run(t.Context(), golang.ID, verification.PlanRequest{Target: target, Capability: golang.Build, BaseExecution: base}, runnerFunc(func(_ context.Context, req verification.ExecutionRequest) (verification.ExecutionResult, error) {
		calls++
		if req.Executable != "go" || len(req.Arguments) == 0 || req.Arguments[0] != "build" {
			t.Fatal(req)
		}
		return verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Sandboxed: true}, nil
	}))
	if err != nil || calls != 1 {
		t.Fatal("existing Go lifecycle not reused", calls, err)
	}
}
