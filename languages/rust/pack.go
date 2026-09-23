package rust

import (
	"safepatch/internal/sir"
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/internal/pack"
)

func New() (*languagepack.Pack, error) {
	return pack.New(languagepack.Rust, []string{".rs"}, nil, []string{"Cargo.toml", "Cargo.lock"}, map[string]sir.Kind{"function_item": sir.Function, "struct_item": sir.Type, "trait_item": sir.Type, "impl_method": sir.Method, "use_declaration": sir.Dependency})
}
