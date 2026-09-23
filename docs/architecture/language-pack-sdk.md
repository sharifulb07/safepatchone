# LanguagePack SDK

`internal/verification/languagepack` defines one shared Go LanguagePack SDK. Initial
packs live under `languages/` and are composed by `languages.NewRegistry`. There is
one module, source tree and release stream, with no edition-specific binaries.

## Interface and initial support

```go
type LanguagePack interface {
    Descriptor() Descriptor
    Detect(context.Context, verification.Target) (Detection, error)
    Normalize(context.Context, sir.Extraction) (sir.Representation, error)
    Verifiers() []verification.Verifier
}
```

Descriptors expose stable pack ID, version, language and implemented technical
capabilities: `language.detect`, `language.sir.normalize`, `language.verify`.
These are support descriptors, not organization grants. A pack's version is separate
from product version, release channel and edition configuration.

Descriptors now also expose `maturity` using the shared PV0-PV8 vocabulary. The
initial five-pack table below records the original increment. The registry now
includes five more packs; see [current support and maturity](language-support-maturity.md)
and `languages.SupportCatalog` for the complete authoritative report. Unsupported
PV4-PV8 claims are rejected by the current registry rather than inferred from tool
presence. Custom pack descriptors must supply maturity consistent with capabilities.

| Pack ID | Source | Detection | Supplied-concept SIR normalization | Verification |
| --- | --- | --- | --- | --- |
| `go` | `languages/golang` | Implemented | Implemented | Existing Go verifier |
| `javascript` | `languages/javascript` | Implemented | Implemented | UNAVAILABLE |
| `typescript` | `languages/typescript` | Implemented | Implemented | UNAVAILABLE |
| `python` | `languages/python` | Implemented | Implemented | UNAVAILABLE |
| `java` | `languages/java` | Implemented | Implemented | UNAVAILABLE |

All packs use the same immutable Definition/Pack implementation and SIR normalizer.
`languages/internal/pack` shares declarative adapter composition. Go's `NewPack`
wraps the existing verifier, preserving build/test/race/vet/Staticcheck/Govulncheck/
fuzz/benchmark behavior and its trusted tool configuration. Execution remains in the
existing verifier registry and secure Runner lifecycle.

Other packs do not advertise `language.verify` or simulate tool execution.
`VerifierRegistry(id)` returns `ErrUnavailable` for them. This SDK does not introduce
JavaScript/TypeScript/Python/Java test runners, dependency installers, AST extractors,
or detection engines. Normalization is a working adapter boundary for extracted
concepts; it is not source-code parsing.

## Detection

Detection uses the existing bounded `verification.Target` snapshot manifest:

| Pack | Source extensions | Recognized marker | Ambiguous marker without matching source |
| --- | --- | --- | --- |
| Go | `.go` | `go.mod`, `go.work` | None |
| JavaScript | `.js`, `.jsx`, `.mjs`, `.cjs` | None | `package.json` |
| TypeScript | `.ts`, `.tsx`, `.mts`, `.cts` | `tsconfig.json` | `package.json` |
| Python | `.py`, `.pyi` | None | `pyproject.toml`, `requirements.txt` |
| Java | `.java` | None | `pom.xml`, `build.gradle`, `build.gradle.kts` |

Matching is case-sensitive and works across nested directories/multiple modules.
Each pack independently examines the manifest, so mixed-language repositories can
match several packs. Shared ecosystem manifests alone produce UNKNOWN rather than
asserting a particular source language. Recognized source/markers return DETECTED
with a sorted list of matching paths; no matches return NOT_APPLICABLE. Detection
is a project hint, not syntax validation, code completeness or verification PASS.

Repository metadata is validated and treated as data. Detection does not evaluate
package scripts, execute `setup.py`, parse build hooks, invoke tools, access paths or
fetch URLs. The trusted collector must provide the intended complete snapshot;
filename detection does not resolve symlinks or exclude generated/vendor files.

## SIR normalization

Each pack accepts all thirteen canonical SIR concept labels, plus explicit aliases:

- Go: `func_decl`, `receiver_method`, `struct_decl`, `interface_decl`, `type_alias`,
  `import_decl`.
- JavaScript: `function_declaration`, `method_definition`, `class_declaration`,
  `import_declaration`.
- TypeScript: `function_declaration`, `method_declaration`, `class_declaration`,
  `interface_declaration`, `type_alias`, `import_declaration`.
- Python: `function_def`, `async_function_def`, `bound_method`, `class_def`,
  `import_from`.
- Java: `method_declaration`, `class_declaration`, `interface_declaration`,
  `record_declaration`, `import_declaration`.

Aliases are adapter-defined tokens, not claims that a parser is present. Extractors
must explicitly distinguish a method from a function and establish operation/effect
facts. A function name, import, prompt or manifest is not enough to infer a database
operation or other effect. Unknown native concepts fail without partial output.

Every concept's language must match its selected pack. Embedded/generated source
can use any valid SIR source path; normalization does not reclassify it by extension.
The sealed SIR preserves extractor identity/version, tenant, repository/revision,
source hashes, names, locations, containment/targets, actions and extraction coverage.
UNKNOWN/PARTIAL coverage and nondeterminism are never silently upgraded.

The registry validates returned SIR against the supplied extraction. It rejects
scope changes, dropped/added concepts, source-hash replacement, rewritten original
facts or inflated metadata. The normalized Kind is the pack's trusted mapping
decision; registry validation cannot prove an extractor's semantic assertions.
Record the selected pack descriptor alongside stored extraction provenance when
auditing which normalization implementation ran; it does not replace SIR's extractor
producer metadata.

## Registry and safety

`NewRegistry` freezes descriptors and verifier registrations. Duplicate IDs or
languages cannot replace installed code. `Discover` returns independent sorted
copies. A verification capability without actual registered verifiers is invalid.
Registry calls copy mutable inputs/outputs, validate detection states and matched
paths, honor cancellation and redact extension errors while retaining `errors.Is`
causes. Panics become failed operations, not success or leaked panic payloads.

Extensions are trusted compiled Go code; the registry is not a sandbox for arbitrary
plugins and cannot forcibly stop an uncooperative extension. Concurrently used packs
must be safe for concurrent calls. The initial declarative packs and registry support
concurrent reads. Callers must not mutate input DTOs during a call.

Registry size is bounded to 128 packs, definitions to 16 extensions and 16 markers
of each kind, 512 SIR mappings and 64 verifiers. Existing Target, SIR, verifier output
and Runner resource limits remain authoritative. Unsafe paths, duplicate mappings,
malformed inputs, unsupported capabilities and unavailable implementations fail
explicitly. The SDK imports no commercial services; architecture tests enforce that
the verification core cannot depend on features, editions or actor permissions.

## Commercial admission and worker authority

`application.LanguagePackService` binds fixed operations to existing feature groups:

| Operation | Feature | Independent permission |
| --- | --- | --- |
| Metadata-only `Detect` | `verify.basic` | `proof.execute` |
| Advanced `Normalize` | `language.additional_packs` | `proof.execute` |

The normalization group applies consistently to all five packs, including Go.
Basic Go verification keeps its existing `GoVerificationRequirement` mapping; this
milestone does not move build/test access behind advanced language normalization.
No new per-language grants or edition defaults are introduced. The built-in offline
Community provider admits detection and denies this advanced group. Other tenants
need an effective grant, independent actor permission and quota admission.

Every service call resolves tenant/repository ownership, checks entitlement and
permission, then applies the quota hook before work. Normalization additionally
checks SIR tenant/repository identity; detection checks the target repository.
Authentication and binding source revisions to the authorized snapshot are upstream
responsibilities. Repository content cannot select a cheaper operation requirement.

`LanguagePackRequirement` accepts only the concrete detect/normalize operations.
It deliberately does not admit generic `language.verify`: actual verification must
use its own capability requirement before calling the existing verifier registry.
SDK methods and registry discovery are not authorization entrypoints.

Workers use the same fixed requirement with `WorkerOperation` and trusted stored
jobs, rechecking current membership, grants, permissions and quotas at each dispatch
and retry. CLI/frontend/enqueue decisions are insufficient. Metered composition must
reserve and commit/release quota through the existing quota infrastructure.

Revocation prevents new advanced normalization without modifying stored SIR or its
schema. Reading retained artifacts still requires independent storage authorization
and applicable retention policy. All editions retain identical pack internals.

## Composition example

```go
packs, err := languages.NewRegistry(golang.Config{
    GoTool: "go", // trusted Runner tool alias, not a repository command
    Parallelism: 1,
})
if err != nil { return err }
service, err := application.NewLanguagePackService(
    packs, catalog, tenantResolver, entitlements, permissions, quotaAdmission,
)
if err != nil { return err }
detected, err := service.Detect(ctx, actor, scope, "typescript", target)
if err != nil { return err }
// DETECTED identifies metadata matches; it is not verification PASS.
normalized, err := service.Normalize(ctx, actor, scope, "typescript", extraction)
// Handle admission/normalization errors before storing the immutable SIR.
```

Constructing this registry performs no tool lookup/execution or network call.
It validates the explicit Go verifier configuration. Physical tool availability and
sandbox policy remain the Runner/operator's responsibility.

## Tests and remaining scope

Tests cover all five packs and thirteen SIR kinds, mixed source/manifest detection,
ambiguous ecosystem markers, registry collisions, descriptor copies, malformed input,
unmapped concepts, scope/fact poisoning, extension errors/panics and concurrency.
The Go pack test exercises the actual existing verifier lifecycle with a recording
Runner fixture. Existing opt-in native Go tests remain separate.

Admission tests exercise every pack across all five edition labels: entitled access,
non-entitled denial, authorization denial after entitlement and unchanged SIR after
revocation/restoration. Offline Community tests, worker dispatch rechecks and scoped
immutable storage tests verify the actual composed path.

No migrations, new edition bundles, separate binaries, API/CLI routes, hosted
deployment or additional-language execution engines are added. Existing migration
regressions remain in the full suite; live PostgreSQL tests retain their opt-in setup.
