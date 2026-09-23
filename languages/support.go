package languages

import (
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/verification/languagepack"
)

const SupportSchema = "safepatch.language-support.v1"

type OperationSupport struct {
	Capability languagepack.Capability `json:"capability"`
	Feature    feature.Key             `json:"feature"`
	Permission authorization.Key       `json:"permission"`
}
type LanguageSupport struct {
	Language    languagepack.Language    `json:"language"`
	Name        string                   `json:"name"`
	Installed   bool                     `json:"installed"`
	Maturity    languagepack.Maturity    `json:"maturity"`
	Pack        *languagepack.Descriptor `json:"pack,omitempty"`
	Operations  []OperationSupport       `json:"operations"`
	Limitations []string                 `json:"limitations"`
}
type SupportReport struct {
	Schema             string                       `json:"schema"`
	TargetLanguages    int                          `json:"target_languages"`
	InstalledLanguages int                          `json:"installed_languages"`
	Rubric             []languagepack.MaturityStage `json:"rubric"`
	Languages          []LanguageSupport            `json:"languages"`
}

// SupportCatalog joins the roadmap with actual installed descriptors and the
// existing application capability mapping. No runtime grants are synthesized.
// PV0 roadmap entries are never registered as executable packs.
func SupportCatalog(r *languagepack.Registry, features *feature.FeatureCatalog) (SupportReport, error) {
	bad := SupportReport{}
	if r == nil || features == nil {
		return bad, languagepack.ErrInvalidPack
	}
	targets := []struct {
		id   languagepack.Language
		name string
	}{
		{"go", "Go"}, {"javascript", "JavaScript"}, {"typescript", "TypeScript"}, {"python", "Python"}, {"java", "Java"},
		{"rust", "Rust"}, {"csharp", "C#"}, {"kotlin", "Kotlin"}, {"ruby", "Ruby"}, {"php", "PHP"},
		{"c", "C"}, {"cpp", "C++"}, {"swift", "Swift"}, {"scala", "Scala"}, {"clojure", "Clojure"},
		{"fsharp", "F#"}, {"visualbasic", "Visual Basic"}, {"dart", "Dart"}, {"elixir", "Elixir"}, {"erlang", "Erlang"},
		{"haskell", "Haskell"}, {"ocaml", "OCaml"}, {"lua", "Lua"}, {"perl", "Perl"}, {"r", "R"},
		{"julia", "Julia"}, {"objectivec", "Objective-C"}, {"groovy", "Groovy"}, {"fortran", "Fortran"}, {"cobol", "COBOL"},
		{"pascal", "Pascal"}, {"ada", "Ada"}, {"zig", "Zig"}, {"nim", "Nim"}, {"crystal", "Crystal"},
		{"elm", "Elm"}, {"solidity", "Solidity"}, {"sql", "SQL"}, {"bash", "Bash"}, {"powershell", "PowerShell"},
	}
	installed := map[languagepack.Language]languagepack.Descriptor{}
	for _, d := range r.Discover() {
		installed[d.Language] = d
	}
	result := SupportReport{Schema: SupportSchema, TargetLanguages: len(targets), Rubric: languagepack.MaturityRubric(), Languages: []LanguageSupport{}}
	for _, target := range targets {
		entry := LanguageSupport{Language: target.id, Name: target.name, Maturity: languagepack.PV0, Operations: []OperationSupport{}, Limitations: []string{"Roadmap only; no installed detection, normalization or verification support."}}
		if d, ok := installed[target.id]; ok {
			entry.Installed = true
			entry.Maturity = d.Maturity
			entry.Pack = &d
			entry.Limitations = d.Limitations()
			result.InstalledLanguages++
			delete(installed, target.id)
			for _, cap := range d.Capabilities {
				if cap == languagepack.Verify {
					continue
				} // Execution retains each verifier's own capability policy.
				requirement, err := application.LanguagePackRequirement(features, cap)
				if err != nil {
					return bad, err
				}
				entry.Operations = append(entry.Operations, OperationSupport{cap, requirement.Feature, requirement.Permission})
			}
		}
		result.Languages = append(result.Languages, entry)
	}
	if len(installed) != 0 {
		return bad, languagepack.ErrInvalidPack
	} // Never silently omit installed support.
	return result, nil
}
