package javascript

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.JavaScript, []string{".js", ".jsx", ".mjs", ".cjs"}, nil, []string{"package.json"}, map[string]sir.Kind{"function_declaration": sir.Function, "method_definition": sir.Method, "class_declaration": sir.Class, "import_declaration": sir.Dependency})
}
