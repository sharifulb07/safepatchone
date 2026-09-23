package java

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.Java, []string{".java"}, nil, []string{"pom.xml", "build.gradle", "build.gradle.kts"}, map[string]sir.Kind{"method_declaration": sir.Method, "class_declaration": sir.Class, "interface_declaration": sir.Type, "record_declaration": sir.Type, "import_declaration": sir.Dependency})
}
