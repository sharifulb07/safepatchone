package replayadapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"safepatch/internal/replay"
	"safepatch/internal/verification"
)

type runnerFunc func(context.Context, verification.ExecutionRequest) (verification.ExecutionResult, error)

func (f runnerFunc) Run(c context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
	return f(c, r)
}
func TestSandboxProtocolExcludesExpectedAndNeverTrustsProcessClaims(t *testing.T) {
	for _, mode := range []string{"ok", "not_sandboxed", "cleanup", "truncated", "bad_output"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			s := &Sandbox{root: root, template: verification.ExecutionRequest{Executable: "candidate", Mode: verification.Sandbox}}
			s.runner = runnerFunc(func(_ context.Context, r verification.ExecutionRequest) (verification.ExecutionResult, error) {
				raw, err := os.ReadFile(filepath.Join(r.SourceDirectory, "replay.json"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "baseline-secret") || strings.Contains(string(raw), "expected") {
					t.Fatal("candidate saw baseline")
				}
				if r.Mode != verification.Sandbox || len(r.Environment) != 0 {
					t.Fatal("unsafe execution profile")
				}
				result := verification.ExecutionResult{Status: verification.Completed, ExitCode: 0, Sandboxed: true, Stdout: []byte(`{"complete":true,"status":200,"output":{"value":2},"effects":[]}`)}
				switch mode {
				case "not_sandboxed":
					result.Sandboxed = false
				case "cleanup":
					return result, verification.ErrCleanupFailed
				case "truncated":
					result.StdoutTruncated = true
				case "bad_output":
					result.Stdout = []byte(`{"complete":true,"complete":false}`)
				}
				return result, nil
			})
			result, err := s.Execute(t.Context(), replay.Record{Input: json.RawMessage(`{"value":1}`), Expected: replay.Observation{Output: json.RawMessage(`{"private":"baseline-secret"}`)}, Fixtures: []replay.Fixture{}})
			if mode == "ok" {
				if err != nil || !result.Observation.Complete || !result.Stopped {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("unsafe execution accepted")
			}
			if mode == "cleanup" && (result.Stopped || !errors.Is(err, replay.ErrUnsafe)) {
				t.Fatal("uncertain termination lost")
			}
			entries, _ := os.ReadDir(root)
			if len(entries) != 0 {
				t.Fatal("temporary replay data retained")
			}
		})
	}
}
func TestOpenTelemetrySourceLinkNewTraceNoBaggageOrPayloads(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer provider.Shutdown(context.Background())
	o, err := NewObserver(provider)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := baggage.NewMember("secret", "private-token")
	bag, _ := baggage.New(member)
	ctx := baggage.ContextWithBaggage(t.Context(), bag)
	link := replay.TraceLink{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16)}
	ctx, end := o.Start(ctx, replay.Scope{OrganizationID: "org", RepositoryID: "repo"}, "run", "case", link)
	if baggage.FromContext(ctx).Len() != 0 || trace.SpanContextFromContext(ctx).TraceID().String() == link.TraceID {
		t.Fatal("captured trace/baggage inherited")
	}
	end(replay.Unknown)
	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Links) != 1 || spans[0].Links[0].SpanContext.TraceID().String() != link.TraceID || spans[0].Parent.IsValid() {
		t.Fatal("correlation missing")
	}
	for _, a := range spans[0].Attributes {
		if strings.Contains(a.Value.AsString(), "private") || string(a.Key) == "secret" {
			t.Fatal("telemetry privacy leak")
		}
	}
}
