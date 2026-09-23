# Language support maturity and incremental expansion

SafePatch now registers ten LanguagePacks through `languages.NewRegistry` in the
same Go source/release tree. This increment adds Rust, C#, Kotlin, Ruby and PHP.
These are detection and supplied-concept normalization adapters, not new parsers
or executable verification engines. The 40-language target catalog contains 30
additional roadmap entries explicitly marked PV0/unavailable. No placeholder pack
is registered for those entries.

## Maturity rubric

PV is a technical support-depth scale, separate from ProofPlan L0-L4, evidence
PASS/FAIL/UNKNOWN, product version, release channel and commercial edition. Its
version-one definitions come from `languagepack.MaturityRubric`:

| Level | Required depth |
| --- | --- |
| PV0 | Roadmap only; no installed pack |
| PV1 | Bounded source/manifest detection |
| PV2 | Shared SIR normalization of supplied concepts |
| PV3 | Registered verifier execution through the shared SDK and Runner |
| PV4 | Validated source extraction and representative build/test integration coverage |
| PV5 | Validated security and compatibility verification across supported toolchains |
| PV6 | Validated mutation/fuzz/performance coverage and resource isolation |
| PV7 | Validated Replay/runtime correlation with source/proof evidence |
| PV8 | Validated continuous and composed proof lifecycle |

The enum and rubric cover PV0 through PV8 now. Current SDK contracts substantiate
at most PV3; `Registry.New` rejects descriptors claiming a maturity inconsistent
with their installed capabilities. Higher ratings require future conformance work
and an explicit extension of the technical assessment, not a license change or a
hand-edited frontend label. Installed tools alone do not demonstrate that depth.
In particular, Go has optional test/security/fuzz integrations, but that does not
establish the validated extraction/integration coverage required for PV4-PV8.

## Current installed depth

| Language | Maturity | New source extensions | Marker-only uncertainty | Execution |
| --- | --- | --- | --- | --- |
| Go | PV3 | Existing pack | Existing behavior | Existing Go SDK; optional tools may be unavailable |
| JavaScript, TypeScript, Python, Java | PV2 | Existing packs | Existing behavior | Unavailable |
| Rust | PV2 | `.rs` | `Cargo.toml`, `Cargo.lock` | Unavailable |
| C# | PV2 | `.cs`, `.csx` | `global.json`, `Directory.Build.props` | Unavailable |
| Kotlin | PV2 | `.kt`, `.kts` | `build.gradle`, `pom.xml` | Unavailable |
| Ruby | PV2 | `.rb`, `.rbs` | `Gemfile`, `Gemfile.lock` | Unavailable |
| PHP | PV2 | `.php`, `.phtml` | `composer.json`, `composer.lock` | Unavailable |

Matching is case-sensitive. Shared ecosystem markers without source evidence yield
UNKNOWN rather than language certainty. `build.gradle.kts` is detected as a Kotlin
script, while it remains an ambiguous marker for Java. This does not imply that the
application code is Kotlin. Package-manager metadata is never executed or fetched.

Each new adapter maps supplied native concept labels to existing SIR kinds (for
example Rust `function_item`, C# `method_declaration`, Ruby `method_definition`).
It also accepts the shared normalized kind vocabulary. It does not extract an AST,
infer effects from names or upgrade partial/UNKNOWN coverage to complete/PASS.

## Reporting and commercial metadata

Run from the repository root:

```powershell
go run ./tools/language-support
```

The tool emits `safepatch.language-support.v1` JSON with 40 target languages, ten
installed packs, current descriptors/maturity/limitations, nine rubric stages and
operation requirements. It performs no network access, package installation,
verification execution or license lookup. Tool binaries need not exist merely to
report that an adapter is registered; runtime availability is checked separately.

`languages.SupportCatalog(registry, featureCatalog)` joins the roadmap to actual
registry descriptors and reuses `application.LanguagePackRequirement`. Thus:

- Detection requires `verify.basic` plus independent `proof.execute` permission.
- Supplied-concept normalization requires `language.additional_packs` plus
  independent `proof.execute` permission.
- Both continue through the existing quota checker; missing/denied quota admission
  fails closed. No new commercial numeric limits are invented by this increment.
- Go verification retains its existing per-verifier capability/permission policy.

Reports describe installed capability, not whether an organization or actor may
use it. Higher editions do not change technical maturity. The same ten packs remain
in every build; entitlement/configuration controls commercial admission without
forks. Existing subscription bundles, schema, snapshots and stored SIR are unchanged.
No database migration is needed for this additive support metadata.

The remaining roadmap languages are C, C++, Swift, Scala, Clojure, F#, Visual Basic,
Dart, Elixir, Erlang, Haskell, OCaml, Lua, Perl, R, Julia, Objective-C, Groovy,
Fortran, COBOL, Pascal, Ada, Zig, Nim, Crystal, Elm, Solidity, SQL, Bash and PowerShell.
The Go target catalog is authoritative; this list is explanatory documentation.
Dashboard/API consumers should consume the report, not maintain an independent
language/edition availability table. The shared OpenAPI contains its schemas;
no new HTTP route is advertised.

## Validation and boundaries

Tests exercise all ten installed packs, all shared SIR kinds, native aliases,
ambiguous/mixed-language detection, malicious paths/metadata, foreign-language
concepts, immutable descriptors/reports, invalid or inflated maturity claims, and
absence of executable verifiers for PV0/PV2 entries. Existing all-edition admission
tests now cover ten packs, proving entitlement denial, independent permission,
quota denial and preserved SIR after plan changes. Community offline detection
coverage likewise expands to all ten. No separate edition binary is compiled.

PV4-PV8 conformance, source AST extractors, nine non-Go execution adapters and the
remaining 30 roadmap packs are not implemented. Existing release blockers remain;
this incremental maturity report is not production readiness certification.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Focused pack/SDK/architecture checks passed, including
the expanded all-edition boundary tests. The report generator executed successfully
and emitted 40 target entries: 1 PV3, 9 PV2 and 30 PV0. Shared OpenAPI YAML and
maturity schemas parsed successfully. No database migration or release was made.
