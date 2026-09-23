package python

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.Python, []string{".py", ".pyi"}, nil, []string{"pyproject.toml", "requirements.txt"}, map[string]sir.Kind{"function_def": sir.Function, "async_function_def": sir.Function, "bound_method": sir.Method, "class_def": sir.Class, "import_from": sir.Dependency})
}
