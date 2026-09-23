package php

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.PHP, []string{".php", ".phtml"}, nil, []string{"composer.json", "composer.lock"}, map[string]sir.Kind{"function_definition": sir.Function, "method_declaration": sir.Method, "class_declaration": sir.Class, "interface_declaration": sir.Type, "namespace_use_declaration": sir.Dependency})
}
