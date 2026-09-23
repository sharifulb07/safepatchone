package golang

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

// NewPack reuses the existing Go verifier and its explicit Runner/tool policy.
func NewPack(config Config) (*languagepack.Pack, error) {
	v, err := New(config)
	if err != nil {
		return nil, err
	}
	return pack.New(languagepack.Go, []string{".go"}, []string{"go.mod", "go.work"}, nil, map[string]sir.Kind{"func_decl": sir.Function, "receiver_method": sir.Method, "struct_decl": sir.Type, "interface_decl": sir.Type, "type_alias": sir.Type, "import_decl": sir.Dependency}, v)
}
