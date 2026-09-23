# FeatureCatalog

The shared catalog is implemented in `internal/domain/feature`. It contains product capability metadata only. A lookup does not grant an organization a feature, authorize an actor, select an edition/channel, enforce a quota, or prove that a capability is implemented.

The initial 29 definitions below are preserved. The [initial edition bundle milestone](initial-edition-bundles.md#new-stable-capability-identifiers) adds IDs `feat_0030` through `feat_0038` for the newly named capabilities, bringing the catalog to 38 PLANNED definitions. Approved commercial allocations live in the seed migration rather than catalog lookup behavior.

The [offline Community provider](community-entitlement-provider.md) adds `feat_0039` / `evidence.local` for local evidence, bringing the current catalog to 39 PLANNED definitions. It does not grant hosted history, shared proof, or proof-passport features. Migration 000004 publishes the additive Community bundle; earlier identities and bundle versions remain unchanged.

**Software Version != Commercial Edition != Release Channel.** Introduced/deprecated versions are software/catalog lifecycle metadata. There are no edition, billing, tenant, or permission fields in a Feature.

## API

```go
catalog, err := feature.NewBuiltinCatalog(registrationVersion, registrationTime)
if err != nil {
    return err
}
definition, err := catalog.Get(feature.KeyVerifyBasic)
// Use definition as metadata only. Access checks belong to separate services.
```

`NewFeatureCatalog([]Feature)` validates a complete snapshot atomically. `Get(Key)`, `GetByID(string)`, `List()`, and `Len()` expose it. Missing lookups return `ErrFeatureNotFound`; no fallback feature or grant is synthesized. The zero value is an empty catalog. Constructors return no partial catalog on error.

The snapshot is immutable: constructor inputs, lookup results, list results, and dependency slices are copied. Listing and dependency order are deterministic. Read-only concurrent use requires no external lock; callers must not mutate their input slices while a constructor is reading them.

## Metadata contract

| JSON field | Meaning and validation |
| --- | --- |
| `feature_id` | Permanent opaque ID, 1–128 ASCII letters/digits/underscore/hyphen; never recycle an ID |
| `key` | Permanent lowercase ASCII capability key, at most 128 bytes; dotted segments support underscores |
| `name` | Nonempty UTF-8 display name, at most 128 bytes, no control characters or surrounding whitespace |
| `description` | Nonempty UTF-8 description, at most 4096 bytes; newline/tab allowed, other controls and surrounding whitespace rejected |
| `category` | VERIFY, INTENT, GRAPH, PROOF, RUNTIME, AGENT, SECURITY, ENTERPRISE, or PLATFORM |
| `status` | PLANNED, ACTIVE, or DEPRECATED; empty/UNKNOWN values rejected |
| `introduced_version` | Explicit semantic version, including optional prerelease/build metadata, at most 128 bytes |
| `deprecated_version` | Empty unless DEPRECATED; required then, with precedence at or after introduced_version |
| `default_limit_type` | NONE, COUNT, DURATION, BYTES, or CREDITS; metadata about metering shape, not a grant or numeric quota |
| `requires_features` | Unique prerequisite keys; no self references, missing keys, or cycles; sorted in snapshots |
| `created_at` | Explicit nonzero timestamp representable in JSON; normalized to UTC in snapshots |

All eleven fields have explicit snake_case JSON names. Empty dependencies serialize as `[]`; no deprecation serializes as an empty string. Deserializing a Feature does not itself validate it: pass decoded snapshots through `NewFeatureCatalog` before use. No upload/JSON parsing endpoint is introduced; a future adapter must also bound payload bytes before decoding.

Semantic-version comparison ignores build metadata, orders numeric prerelease identifiers numerically without integer overflow, and rejects malformed/leading-zero versions. Equal introduction and deprecation versions are allowed. No wall-clock call or assumed release version is hidden in catalog construction.

Limits bound catalog construction to 4096 features and 64 direct prerequisites per feature. Dependency validation is iterative. Errors support `errors.Is` using `ErrInvalidFeature`, `ErrDuplicateFeature`, `ErrUnknownDependency`, `ErrDependencyCycle`, and `ErrFeatureNotFound`.

## Stable built-in vocabulary

The requested naming rule conflicts with six explicitly required identifiers: `business.invariants` and the five `enterprise.*` keys. This implementation preserves those exact identifiers as capability-namespace exceptions, consistent with the existing architecture document. It rejects Community, Developer, Team, Business, and Enterprise as dot/underscore-delimited tokens in other keys. This is lexical key validation, not a runtime comparison of an organization's plan. `enterprise.custom` and `enterprise.sso.extra`, for example, are rejected.

All 29 initial definitions are **PLANNED**, use **NONE** as their default metering type, and have no prescribed prerequisites. NONE does **not** mean unlimited access. No feature-to-edition assignments, dependency policy, quotas, or implementation readiness are inferred from feature names. Categories group metadata; ENTERPRISE is not an edition access check. The LTS capability describes support eligibility and does not select the deployment's channel.

| Permanent ID | Key | Category |
| --- | --- | --- |
| feat_0001 | `verify.basic` | VERIFY |
| feat_0002 | `verify.security` | SECURITY |
| feat_0003 | `verify.api` | VERIFY |
| feat_0004 | `verify.polyglot` | VERIFY |
| feat_0005 | `intent.basic` | INTENT |
| feat_0006 | `intent.advanced` | INTENT |
| feat_0007 | `intent.specforge` | INTENT |
| feat_0008 | `graph.local` | GRAPH |
| feat_0009 | `graph.organization` | GRAPH |
| feat_0010 | `graph.cross_repo` | GRAPH |
| feat_0011 | `proof.planner` | PROOF |
| feat_0012 | `proof.passport` | PROOF |
| feat_0013 | `proof.living` | PROOF |
| feat_0014 | `testtrust` | PROOF |
| feat_0015 | `runtime.replay` | RUNTIME |
| feat_0016 | `runtime.behaviordna` | RUNTIME |
| feat_0017 | `runtime.shadow` | RUNTIME |
| feat_0018 | `business.invariants` | VERIFY |
| feat_0019 | `incident.antibodies` | SECURITY |
| feat_0020 | `agent.provenance` | AGENT |
| feat_0021 | `agent.mcp_inventory` | AGENT |
| feat_0022 | `agent.mcp_gateway` | AGENT |
| feat_0023 | `agent.safelease` | AGENT |
| feat_0024 | `agent.temporalguard` | AGENT |
| feat_0025 | `enterprise.sso` | ENTERPRISE |
| feat_0026 | `enterprise.audit` | ENTERPRISE |
| feat_0027 | `enterprise.self_hosted` | ENTERPRISE |
| feat_0028 | `enterprise.airgap` | ENTERPRISE |
| feat_0029 | `enterprise.lts` | ENTERPRISE |

## Lifecycle and persistence boundary

Built-in IDs are fixed literals, independent of iteration order, software version, clock, edition, or channel. Callers supply the initial registration version/time so seeding is reproducible. Persist those original values when a storage milestone is implemented; do not overwrite registration metadata at each startup.

The catalog has no mutation, deletion, billing, or subscription API. Deprecation retains identity and description; it does not delete records. A new snapshot can be built without mutating its predecessor. This constructor validates internal consistency, not historical changes against a database: future persistence must enforce immutable ID/key bindings, preserve references, and apply the one shared migration history. There is no database, migration runner, or SQL change in this milestone.

An organization plan change must change grants independently of the catalog. Premium entitlement/authorization access tests and plan-change data-preservation tests belong to the future access-service and persistence milestones; metadata entries do not implement premium workflows. No `Has` method is provided here to confuse catalog membership with `EntitlementService.Has(...)`.

## Validation and remaining scope

Tests pin all 29 ID/key pairs and eleven JSON fields, validate lifecycle metadata and SemVer, reject hostile/edition-tagged keys, check duplicate/dangling/cyclic dependencies, exercise maximum graph size, preserve copies through concurrent reads, and round-trip JSON snapshots. Architecture tests still prohibit domain imports of billing and infrastructure. The key fuzz target also runs its seed corpus during ordinary tests.

Run `go test ./...` and `go test -race ./...`. No database migration tests are applicable until persistence exists. EntitlementService, PermissionService, runtime engines, API endpoints, licensing, and commercial allocation remain unimplemented.

Validation for this milestone: `go test ./...`, `go test -race ./...`, and `go vet ./...` passed on Go 1.26.5, Windows/amd64. Go formatting and Markdown relative-link checks also passed. The ordinary test runs exercised fuzz seeds; no extended fuzz campaign was run.

Next recommended milestone: implement tenant-scoped entitlement and permission contracts that consume stable feature keys, keeping catalog existence separate from access decisions.
