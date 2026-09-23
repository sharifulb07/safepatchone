package kotlin

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.Kotlin, []string{".kt", ".kts"}, nil, []string{"build.gradle", "pom.xml"}, map[string]sir.Kind{"function_declaration": sir.Function, "class_declaration": sir.Class, "object_declaration": sir.Type, "import_header": sir.Dependency})
}
