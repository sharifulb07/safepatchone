package typescript

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.TypeScript, []string{".ts", ".tsx", ".mts", ".cts"}, []string{"tsconfig.json"}, []string{"package.json"}, map[string]sir.Kind{"function_declaration": sir.Function, "method_declaration": sir.Method, "class_declaration": sir.Class, "interface_declaration": sir.Type, "type_alias": sir.Type, "import_declaration": sir.Dependency})
}
