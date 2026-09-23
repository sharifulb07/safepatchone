package languages_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/sir"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages"
	"slices"
	"testing"
	"time"
)

func TestIncrementalLanguageSupportReport(t *testing.T) {
	r := registry(t)
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	report, err := languages.SupportCatalog(r, fc)
	if err != nil {
		t.Fatal(err)
	}
	if report.Schema != languages.SupportSchema || report.TargetLanguages != 40 || report.InstalledLanguages != 10 || len(report.Languages) != 40 || len(report.Rubric) != 9 {
		t.Fatal("incorrect maturity counts")
	}
	raw, _ := json.Marshal(report)
	seen := map[languagepack.Language]bool{}
	counts := map[languagepack.Maturity]int{}
	for _, s := range report.Languages {
		if seen[s.Language] || len(s.Limitations) == 0 {
			t.Fatal("invalid catalog entry")
		}
		seen[s.Language] = true
		counts[s.Maturity]++
		if !s.Installed {
			if s.Maturity != languagepack.PV0 || s.Pack != nil || len(s.Operations) != 0 {
				t.Fatal("roadmap advertised as installed")
			}
			if _, err := r.VerifierRegistry(string(s.Language)); !errors.Is(err, languagepack.ErrPackNotFound) {
				t.Fatal("phantom verifier")
			}
			continue
		}
		if s.Pack == nil || s.Maturity != s.Pack.Maturity || len(s.Operations) != 2 {
			t.Fatal("descriptor drift")
		}
		for _, o := range s.Operations {
			expected := feature.KeyVerifyBasic
			if o.Capability == languagepack.NormalizeSIR {
				expected = feature.KeyLanguageAdditionalPacks
			}
			if o.Feature != expected || o.Permission != authorization.KeyProofExecute {
				t.Fatal("commercial mapping drift", o)
			}
		}
		if s.Language == languagepack.Go {
			if s.Maturity != languagepack.PV3 {
				t.Fatal("Go depth")
			}
		} else if s.Maturity != languagepack.PV2 || slices.Contains(s.Pack.Capabilities, languagepack.Verify) {
			t.Fatal("false verification parity")
		}
	}
	if counts[languagepack.PV0] != 30 || counts[languagepack.PV2] != 9 || counts[languagepack.PV3] != 1 {
		t.Fatal(counts)
	}
	report.Languages[0].Pack.Capabilities[0] = "poison"
	report.Languages[0].Limitations[0] = "poison"
	report.Rubric[0].Requirement = "poison"
	again, err := languages.SupportCatalog(r, fc)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := json.Marshal(again)
	if !bytes.Equal(raw, other) {
		t.Fatal("mutable support catalog")
	}
	if _, err := languages.SupportCatalog(nil, fc); err == nil {
		t.Fatal("nil registry")
	}
	if _, err := languages.SupportCatalog(r, nil); err == nil {
		t.Fatal("nil features")
	}
}

func TestNewLanguageAliasesAndAmbiguousDetection(t *testing.T) {
	r := registry(t)
	cases := []struct {
		id, file, marker, alias string
		kind                    sir.Kind
	}{
		{"rust", "src/lib.rs", "Cargo.toml", "function_item", sir.Function},
		{"csharp", "src/Main.cs", "global.json", "method_declaration", sir.Method},
		{"kotlin", "src/Main.kt", "build.gradle", "function_declaration", sir.Function},
		{"ruby", "lib/main.rb", "Gemfile", "method_definition", sir.Method},
		{"php", "src/index.php", "composer.json", "function_definition", sir.Function},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			target := verification.Target{Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}, Files: []string{tc.marker}, Metadata: map[string]string{tc.marker: "untrusted install: curl https://internal/credentials"}}
			d, err := r.Detect(t.Context(), tc.id, target)
			if err != nil || d.State != verification.DetectionUnknown || len(d.Files) != 0 {
				t.Fatal("ambiguous marker inflated", d, err)
			}
			target.Files = append(target.Files, tc.file)
			d, err = r.Detect(t.Context(), tc.id, target)
			if err != nil || d.State != verification.Detected || !slices.Equal(d.Files, []string{tc.file}) {
				t.Fatal(d, err)
			}
			in := input(tc.id, tc.file)
			in.Concepts[0].NativeKind = tc.alias
			out, err := r.Normalize(t.Context(), tc.id, in)
			if err != nil {
				t.Fatal(err)
			}
			f, _ := out.Fields()
			if f.Nodes[0].Kind != tc.kind || f.Coverage != sir.CoveragePartial {
				t.Fatal("inflated extraction", f)
			}
			target.Files = []string{"../secret"}
			if _, err := r.Detect(t.Context(), tc.id, target); err == nil {
				t.Fatal("traversal")
			}
			in.Concepts[0].Language = "other"
			if _, err := r.Normalize(t.Context(), tc.id, in); err == nil {
				t.Fatal("foreign language")
			}
		})
	}
	// Shared JVM markers do not imply either language; Kotlin script is concrete.
	target := verification.Target{Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}, Files: []string{"build.gradle.kts"}}
	java, err := r.Detect(t.Context(), "java", target)
	if err != nil || java.State != verification.DetectionUnknown {
		t.Fatal(java, err)
	}
	kotlin, err := r.Detect(t.Context(), "kotlin", target)
	if err != nil || kotlin.State != verification.Detected {
		t.Fatal(kotlin, err)
	}
}
