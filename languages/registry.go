// Package languages composes every installed pack in one source/release tree.
package languages

import (
	"safepatch/internal/verification/languagepack"
	"safepatch/languages/csharp"
	"safepatch/languages/golang"
	"safepatch/languages/java"
	"safepatch/languages/javascript"
	"safepatch/languages/kotlin"
	"safepatch/languages/php"
	"safepatch/languages/python"
	"safepatch/languages/ruby"
	"safepatch/languages/rust"
	"safepatch/languages/typescript"
)

func NewRegistry(goConfig golang.Config) (*languagepack.Registry, error) {
	goPack, err := golang.NewPack(goConfig)
	if err != nil {
		return nil, err
	}
	packs := []languagepack.LanguagePack{goPack}
	for _, create := range []func() (*languagepack.Pack, error){javascript.New, typescript.New, python.New, java.New, rust.New, csharp.New, kotlin.New, ruby.New, php.New} {
		p, err := create()
		if err != nil {
			return nil, err
		}
		packs = append(packs, p)
	}
	return languagepack.NewRegistry(packs...)
}
