# Language adapters

Language-specific repository parsing and tool integration belong here. Reuse shared domain/application contracts; do not add nested Go modules or edition variants. The [Go adapter](golang) implements build, test, race, vet, Staticcheck, Govulncheck, fuzz and benchmark verification through the shared SDK and Runner. See [Go verification](../docs/architecture/go-verification.md) for provisioning and usage.

The [LanguagePack SDK](../docs/architecture/language-pack-sdk.md) composes Go,
JavaScript, TypeScript, Python, Java, Rust, C#, Kotlin, Ruby and PHP packs in
`languages.NewRegistry`. All ten
support metadata detection and normalization of supplied concepts into shared SIR.
Go reuses its existing verifier; other language verification engines and AST
extractors remain unavailable. Advanced normalization uses the existing
`language.additional_packs` entitlement group at the application boundary.

See [support maturity](../docs/architecture/language-support-maturity.md) for the
PV0-PV8 rubric and the 40-language roadmap. Only ten packs are installed: Go is
PV3, nine are PV2, and the other 30 roadmap entries are PV0/unavailable. Emit the
authoritative JSON report with `go run ./tools/language-support`.
