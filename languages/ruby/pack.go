package ruby

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.Ruby, []string{".rb", ".rbs"}, nil, []string{"Gemfile", "Gemfile.lock"}, map[string]sir.Kind{"method_definition": sir.Method, "class_definition": sir.Class, "module_definition": sir.Type, "require_statement": sir.Dependency})
}
