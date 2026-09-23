// Package pack shares declarative adapter composition across language packs.
package pack

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification"
	"safepatch/internal/verification/languagepack"
)

// New accepts normalized native concept labels plus explicit adapter aliases.
// It does not infer effects from source text, filenames, call names or prompts.
func New(language languagepack.Language, extensions, markers, ambiguous []string, aliases map[string]sir.Kind, verifiers ...verification.Verifier) (*languagepack.Pack, error) {
	mappings := []sir.Mapping{}
	for _, kind := range sir.Kinds() {
		mappings = append(mappings, sir.Mapping{Language: string(language), NativeKind: string(kind), Kind: kind})
	}
	for native, kind := range aliases {
		mappings = append(mappings, sir.Mapping{Language: string(language), NativeKind: native, Kind: kind})
	}
	return languagepack.New(languagepack.Definition{ID: string(language), Version: "1.0.0", Language: language, Extensions: extensions, Markers: markers, AmbiguousMarkers: ambiguous, Mappings: mappings, Verifiers: verifiers})
}
