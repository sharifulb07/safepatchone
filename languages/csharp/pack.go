package csharp

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.CSharp, []string{".cs", ".csx"}, nil, []string{"global.json", "Directory.Build.props"}, map[string]sir.Kind{"method_declaration": sir.Method, "class_declaration": sir.Class, "interface_declaration": sir.Type, "record_declaration": sir.Type, "using_directive": sir.Dependency})
}
