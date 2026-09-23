package languagepack_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/sir"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
)

func definition() languagepack.Definition {
	return languagepack.Definition{ID: "python", Language: languagepack.Python, Version: "1.0.0", Extensions: []string{".py"}, AmbiguousMarkers: []string{"pyproject.toml"}, Mappings: []sir.Mapping{{Language: "python", NativeKind: "function_def", Kind: sir.Function}}}
}
func extraction() sir.Extraction {
	return sir.Extraction{Metadata: sir.Metadata{Schema: sir.Schema, TenantID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "revision"}, Producer: "parser", ProducerVersion: "1.0.0", Coverage: sir.CoverageUnknown}, Files: []sir.SourceFile{{Path: "main.py", ContentHash: evidence.Digest(nil)}}, Concepts: []sir.Concept{{ID: "function", Language: "python", NativeKind: "function_def", Name: "main", Source: evidence.SourceLocation{Path: "main.py"}}}}
}
func target() verification.Target {
	return verification.Target{Subject: extraction().Subject, Files: []string{"main.py"}}
}

func TestDefinitionRegistryAndImmutability(t *testing.T) {
	d := definition()
	p, err := languagepack.New(d)
	if err != nil {
		t.Fatal(err)
	}
	d.Extensions[0] = ".exe"
	d.Mappings[0].Kind = sir.Class
	r, err := languagepack.NewRegistry(p)
	if err != nil {
		t.Fatal(err)
	}
	descriptions := r.Discover()
	descriptions[0].Capabilities[0] = "injected"
	if r.Discover()[0].Capabilities[0] == "injected" {
		t.Fatal("mutable descriptor")
	}
	detected, err := r.Detect(t.Context(), "python", target())
	if err != nil || detected.State != verification.Detected {
		t.Fatal(detected, err)
	}
	for _, scenario := range []struct {
		files []string
		state verification.DetectionState
	}{{[]string{"pyproject.toml"}, verification.DetectionUnknown}, {[]string{"other.go"}, verification.NotApplicable}} {
		in := target()
		in.Files = scenario.files
		got, err := r.Detect(t.Context(), "python", in)
		if err != nil || got.State != scenario.state {
			t.Fatal(got, err)
		}
	}
	normalized, err := r.Normalize(t.Context(), "python", extraction())
	if err != nil {
		t.Fatal(err)
	}
	f, _ := normalized.Fields()
	if f.Nodes[0].Kind != sir.Function || f.Coverage != sir.CoverageUnknown || f.Deterministic {
		t.Fatal("configuration mutation or inflated claims")
	}
	if _, err := r.VerifierRegistry("python"); !errors.Is(err, languagepack.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := r.Detect(t.Context(), "missing", target()); !errors.Is(err, languagepack.ErrPackNotFound) {
		t.Fatal(err)
	}
	if _, err := languagepack.NewRegistry(p, p); !errors.Is(err, languagepack.ErrInvalidPack) {
		t.Fatal(err)
	}
	if _, err := languagepack.NewRegistry(); err == nil {
		t.Fatal("empty registry")
	}
}

func TestInvalidPackDefinitionsAndInputs(t *testing.T) {
	for _, scenario := range []string{"id", "version", "language", "extension", "duplicate_extension", "marker", "duplicate_marker", "foreign_mapping", "oversized_mapping"} {
		t.Run(scenario, func(t *testing.T) {
			d := definition()
			switch scenario {
			case "id":
				d.ID = "../secret"
			case "version":
				d.Version = "latest"
			case "language":
				d.Language = "Python"
			case "extension":
				d.Extensions = []string{"../x"}
			case "duplicate_extension":
				d.Extensions = []string{".py", ".py"}
			case "marker":
				d.Markers = []string{"dir/setup.py"}
			case "duplicate_marker":
				d.Markers = []string{"pyproject.toml"}
			case "foreign_mapping":
				d.Mappings[0].Language = "java"
			case "oversized_mapping":
				d.Mappings = make([]sir.Mapping, sir.MaxMappings+1)
			}
			if _, err := languagepack.New(d); err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
	p, _ := languagepack.New(definition())
	r, _ := languagepack.NewRegistry(p)
	bad := target()
	bad.Files = []string{"../secret"}
	if _, err := r.Detect(t.Context(), "python", bad); err == nil {
		t.Fatal("traversal")
	}
	in := extraction()
	in.Concepts[0].Language = "java"
	if _, err := r.Normalize(t.Context(), "python", in); err == nil {
		t.Fatal("foreign language")
	}
	in = extraction()
	in.Concepts[0].NativeKind = "unmapped"
	if _, err := r.Normalize(t.Context(), "python", in); !errors.Is(err, sir.ErrUnknownConcept) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if _, err := r.Detect(ctx, "python", target()); err == nil {
			t.Fatal("context ignored")
		}
		if _, err := r.Normalize(ctx, "python", extraction()); err == nil {
			t.Fatal("context ignored")
		}
	}
}

type hostile struct {
	base     *languagepack.Pack
	scenario string
}

func (h hostile) Descriptor() languagepack.Descriptor {
	d := h.base.Descriptor()
	if h.scenario == "invalid_descriptor" {
		d.Capabilities = append(d.Capabilities, languagepack.Verify)
	}
	return d
}
func (h hostile) Verifiers() []verification.Verifier { return nil }
func (h hostile) Detect(ctx context.Context, in verification.Target) (languagepack.Detection, error) {
	switch h.scenario {
	case "panic":
		panic("secret")
	case "error":
		return languagepack.Detection{}, errors.New("secret")
	case "foreign_file":
		return languagepack.Detection{State: verification.Detected, Files: []string{"foreign.py"}}, nil
	case "bad_state":
		return languagepack.Detection{State: "PASS"}, nil
	case "mutation":
		in.Files[0] = "changed.py"
		return languagepack.Detection{State: verification.NotApplicable}, nil
	}
	return h.base.Detect(ctx, in)
}
func (h hostile) Normalize(ctx context.Context, in sir.Extraction) (sir.Representation, error) {
	if h.scenario == "panic" {
		panic("secret")
	}
	if h.scenario == "error" {
		return sir.Representation{}, errors.New("secret")
	}
	r, err := h.base.Normalize(ctx, in)
	if err != nil {
		return r, err
	}
	f, _ := r.Fields()
	switch h.scenario {
	case "tenant":
		f.TenantID = "other"
	case "revision":
		f.Subject.Revision = "other"
	case "coverage":
		f.Coverage = sir.CoverageComplete
	case "drop":
		f.Nodes = nil
	case "source_hash":
		f.Files[0].ContentHash = evidence.Digest([]byte("forged"))
	case "name":
		f.Nodes[0].Name = "forged"
	}
	return sir.New(ctx, f)
}

func TestRegistryRejectsExtensionPoisoningAndRedactsErrors(t *testing.T) {
	p, _ := languagepack.New(definition())
	for _, scenario := range []string{"panic", "error", "tenant", "revision", "coverage", "drop", "source_hash", "name"} {
		t.Run(scenario, func(t *testing.T) {
			r, _ := languagepack.NewRegistry(hostile{p, scenario})
			out, err := r.Normalize(t.Context(), "python", extraction())
			if err == nil || out.ContentHash() != "" || strings.Contains(err.Error(), "secret") {
				t.Fatal(out, err)
			}
		})
	}
	for _, scenario := range []string{"panic", "error", "foreign_file", "bad_state"} {
		r, _ := languagepack.NewRegistry(hostile{p, scenario})
		out, err := r.Detect(t.Context(), "python", target())
		if err == nil || out.State != verification.DetectionUnknown || strings.Contains(err.Error(), "secret") {
			t.Fatal(out, err)
		}
	}
	if _, err := languagepack.NewRegistry(hostile{p, "invalid_descriptor"}); err == nil {
		t.Fatal("fake verifier availability")
	}
	r, _ := languagepack.NewRegistry(hostile{p, "mutation"})
	in := target()
	if _, err := r.Detect(t.Context(), "python", in); err != nil || in.Files[0] != "main.py" {
		t.Fatal("mutated input", err)
	}
}

func TestRegistryConcurrentCalls(t *testing.T) {
	p, _ := languagepack.New(definition())
	r, _ := languagepack.NewRegistry(p)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := r.Detect(t.Context(), "python", target()); err != nil {
				t.Error(err)
			}
			if _, err := r.Normalize(t.Context(), "python", extraction()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
