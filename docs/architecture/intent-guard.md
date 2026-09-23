# Deterministic IntentGuard

`internal/intent/guard.IntentGuard` evaluates the same IntentContract for every
edition. It consumes explicit semantic facts, a Git change manifest and optional
SpecForge properties with immutable verification evidence. It produces intent
compliance, scope drift, protected-property assessments and evidence IDs. It does
not infer natural-language behavior or authorize production.

## Inputs

| Input | Contract |
| --- | --- |
| `Identity` | Authenticated tenant, repository, change, run and evaluation time |
| `Contract` | Existing immutable twelve-field `intent.IntentContract` |
| `SemanticChanges` | Scoped, explicit path-to-component mappings, diff hash and trusted completeness/determinism flags |
| `GitDiff` | Bounded raw Git diff bytes and a trusted completeness flag |
| `Properties` | Optional immutable `spec.Specification`, already generated under its own admission rules |
| `Evidence` | Immutable evidence used to evaluate the SpecForge properties |

There was no semantic-change implementation in this checkout. The new
`SemanticChanges` DTO contains only facts needed for deterministic component
checks; it does not introduce a speculative language-specific SIR. Each path has
one exact logical component identifier, supplied by trusted repository analysis.
The evaluator cannot certify that a caller has classified a file correctly.

The semantic scope must match the input identity and its diff hash must equal
SHA256 of the exact Git bytes. The semantic content hash includes scope, diff hash,
completeness, determinism and mappings sorted by path. Duplicate paths, unsafe
identifiers and mappings for paths absent from the diff are errors. A missing
mapping, incomplete capture or nondeterministic mapping prevents compliance PASS.
Hashes and flags are not authentication: do not accept scanner/LLM claims as trusted
collection provenance. Input DTOs must not be mutated concurrently during evaluation.

## Git input format

The parser accepts Git's [raw NUL-delimited diff format](https://git-scm.com/docs/git-diff#_raw_output_format),
not a textual patch. A trusted collector can request:

```text
git diff --raw --no-abbrev --no-ext-diff --no-textconv -z <base> <head> --
```

Supply validated revisions as separate Runner arguments, never shell interpolation.
Collect the entire selected change, with successful exit status and untruncated
output, before marking it complete. A raw diff reports tracked changes: untracked
files must first be included in the trusted snapshot. Path filters, ignored files,
working-tree races and collector failure cannot silently become complete coverage.

The parser supports additions, deletions, modifications, type changes, renames and
copies with full SHA-1/SHA-256 object identifiers. Both rename/copy paths participate
in conservative scope checks. Unmerged/unknown statuses, combined diffs, abbreviated
hashes, invalid modes, unsafe paths and malformed/truncated records are errors.
Empty complete output represents no changed tracked paths.

Symbolic-link and submodule mode records are treated as changed logical paths.
The guard does not follow links, inspect submodule contents or certify a filesystem
sandbox. Existing secure Runner/snapshot confinement remains required upstream.

## Deterministic rules

Components use the IntentContract's exact, case-sensitive logical identifiers;
there is no inferred directory prefix, glob or regular-expression matching.

- A mapped component in `forbidden_components` is scope drift. An exact forbidden
  identifier matching a changed path is also drift, even if a mapping relabels it.
- When `allowed_components` is nonempty, a mapped component outside that set is drift.
- Missing coverage produces UNKNOWN. Observed violations still take precedence as
  FAIL when other inputs are incomplete.
- With complete inputs and no component restrictions, scope is SKIPPED with
  `NO_SCOPE_CONSTRAINTS`. It does not imply unrestricted production authorization.

All eleven declaration lists are enumerated using SpecForge's stable assertion IDs.
Allowed/forbidden component declarations can be checked directly from scope facts.
Other declarations, including protected properties, require a bound SpecForge check.
Unbound declarations stay UNKNOWN. A goal-only contract cannot produce compliance
PASS; the goal is context, not a proven assertion.

IntentGuard evaluates the sealed SpecForge artifact itself. It does not accept a
mutable client-supplied PASS report. The artifact must match the current contract,
tenant and repository. The existing policy engine enforces evidence scope, time,
uniqueness, determinism and configured confidence. Matching evidence must bind all
three current hashes in `input_hashes`:

```text
intent_contract
git_diff
semantic_changes
```

The SpecForge engine checks the contract hash; IntentGuard additionally checks the
diff and semantic hashes for every observation used by a property check. Wrong or
missing bindings are ERROR. Evidence without a SpecForge artifact is rejected.
Irrelevant observations still undergo the existing envelope/scope validation.

All matching observations must PASS. FAIL or WARN evidence fails a required check;
missing, ERROR, SKIPPED, UNAVAILABLE, UNKNOWN, nondeterministic or insufficiently
confident evidence cannot establish it. Forbidden-behavior checks must verify
absence; failed observations are never inverted into success. A provided failing
component property cannot be hidden by a passing direct scope check.

Overall compliance combines every declaration and scope result. FAIL takes
precedence, and any unresolved requirement or incomplete collection prevents PASS.
Protected properties report individual assertion IDs, statuses, codes and source
evidence IDs. Their statuses describe their checks; overall compliance can remain
UNKNOWN due to missing scope facts even when an individual property is verified.

## Outputs and immutable evidence

`guard.Result` exposes:

| Field | Meaning |
| --- | --- |
| `intent_compliance` | Overall declaration assessment and machine-readable code |
| `scope_drift` | Scope status, code and ordered path/component violations |
| `protected_properties` | Assessment of every protected-property assertion |
| `evidence_ids` | Three generated immutable evidence IDs |
| Go `Evidence` field | The corresponding immutable Evidence values, excluded from result JSON |

The three shared evidence types are `intent.compliance`, `intent.scope_drift` and
`intent.protected_properties`, produced by `intentguard` version `1.0.0`. Repository
identity lives in the payload; tenant/change/run identity uses the existing schema.
Each record binds the contract, diff, semantic facts, SpecForge artifact, input
evidence hashes and diff completeness. Evaluation time is supplied explicitly.
Identical inputs and time produce identical evidence IDs/content hashes across
editions. New input/time creates new records without rewriting history.

Payloads describe the corresponding assessment. The protected-properties aggregate
is SKIPPED when none were declared. Confidence is 1 for a resolved deterministic
PASS/FAIL computation and 0 for unresolved/skipped output; it does not measure the
authenticity of upstream data or natural-language correctness. These observations
are never production approval tokens.

Result DTOs are mutable inspection data; changing them does not change sealed
Evidence. The caller must store the returned records through existing authorized
immutable storage before treating IDs as persisted. Evaluation performs no storage
side effect. On invalid input or cancellation it returns an error and ERROR
assessments with no partially emitted evidence IDs.

## Admission and edition boundaries

`application.NewIntentGuardService(...)` fixes deterministic evaluation to
`intent.basic` plus the independent `proof.execute` permission. It uses the existing
tenant resolver, EntitlementService, PermissionService and quota hook on every call,
then verifies that input identity matches the authorized repository scope. All five
editions run this same implementation. The offline Community provider can admit it
without an account, subscription or network.

Workers use `IntentRequirement(catalog, feature.KeyIntentBasic)` with the existing
`WorkerOperation` and trusted stored job provenance, or dispatch through the service
with the stored authenticated actor. They must recheck at dispatch/retry, not rely on
frontend/CLI or enqueue decisions. Quota reservations and commit/release remain the
responsibility of existing metered-work composition.

Consuming an existing immutable SpecForge artifact does not generate a new one.
Generation still requires `intent.specforge`; advanced SpecDoctor analysis requires
`intent.advanced`. Downgrades do not change contract fields, stored specifications,
required checks or historical evidence. Organization-level template management and
advanced semantic inference are not implemented or silently routed through basic
evaluation. Future management capabilities must be registered and guarded separately.

Retention remains an existing storage/entitlement policy, not a different evaluator.
The generated evidence can be consumed by the existing shared policy engine;
central policy management retains its separate capabilities and actor permissions.
This milestone changes no edition bundles, limits, catalog identities or migrations.

## Usage and validation

Trusted application composition calls:

```go
service, err := application.NewIntentGuardService(
    catalog, tenantResolver, entitlements, permissions, quotaAdmission,
)
if err != nil { return err }
result, err := service.Evaluate(ctx, authenticatedActor, authorizedScope, input)
if err != nil { return err }
// Persist result.Evidence through authorized immutable storage.
// Use result.IntentCompliance.Status; never map UNKNOWN to PASS.
```

Safety limits are 2 MiB diff bytes, 512 records/unique paths/mappings and 512 bytes
per path/component, plus existing contract, specification, policy and Evidence
bounds. Oversized output evidence fails rather than dropping violations. There is
no shell, filesystem access, archive extraction, URL fetch or prompt execution in
the core. Parsing/iteration is bounded and cancellation is checked during work.

Tests cover raw-diff formats and hostile inputs, rename/copy scope, all verifier
statuses, missing properties/mappings, stale/foreign evidence, immutable deterministic
IDs, concurrent evaluation, admission/provider/quota failures, all five editions,
offline Community, revocation/restoration and authoritative worker rechecks. Native
Git integration captures a real staged diff and stores/decodes the resulting Evidence
through existing scoped immutable storage. Parser fuzz seeds run in the ordinary
suite. Existing migration regressions remain applicable; live PostgreSQL checks
retain their opt-in environment requirement.

No CLI/API route, repository semantic analyzer, template store, hosted worker
deployment, semantic inference model or production approval workflow is added.
