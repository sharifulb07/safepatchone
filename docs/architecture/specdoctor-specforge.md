# SpecDoctor and SpecForge

`internal/spec` implements one shared SpecDoctor and SpecForge over the canonical
IntentContract. Neither accepts an edition, entitlement service, actor permission,
network client or process runner. Architecture tests prohibit commercial dependencies
in this package. Admission uses `application.SpecService` and existing worker guards.

## Operations and admission

| Application operation | Required feature | Required permission | Shared behavior |
| --- | --- | --- | --- |
| `SpecService.Validate` | `intent.basic` | `proof.execute` | Validate explicit declarations |
| `SpecService.Analyze` | `intent.advanced` | `proof.execute` | Ambiguity scoring and review-only suggestions |
| `SpecService.Generate` | `intent.specforge` | `proof.execute` | Generate an executable declarative specification |

The service fixes the key for each operation. Request text cannot choose a cheaper
capability. Every call resolves tenant/repository ownership, checks current feature
ownership, independently checks actor permission, and applies the injected quota
policy before doing work. Dependencies, cancellation and access failures fail closed.
Existing entitlement errors retain `FEATURE_NOT_ENTITLED` through the shared transport
error mapping. No sales text or edition-based behavior is added.

Community's built-in provider permits basic validation without an account, payment
provider or network. It denies analysis and generation. This milestone changes no
bundles, limits, grants, catalog identities or historical edition matrices. Allocated
forward-looking grants are not proof that a cloud workflow is deployed. In particular,
this implementation does not automatically grant `intent.specforge` to an edition.

Workers select the matching `IntentRequirement` at trusted composition and use
`WorkerOperation` to reload the stored job and recheck access before invoking the
shared core. Alternatively, dispatch through the corresponding SpecService method
with the authenticated stored actor and scope. Client/enqueue checks are insufficient.
Quota composition must reserve metered work and handle commit/release using the
existing infrastructure; a successful quota check is not automatically a reservation.

## SpecDoctor

`SpecDoctor.Validate(ctx, raw)` uses the existing strict IntentContract decoder. It
checks all twelve fields, exact declaration conflicts, component identifier safety,
Unicode, duplicate keys and resource bounds. The result includes version, contract
hash, status and a stable diagnostic code. `VALID_EXPLICIT_CONSTRAINTS` / PASS means
the declaration is structurally valid. It makes no claim that repository behavior
satisfies the declaration. Invalid declarations return FAIL and an error; invalid
contexts return ERROR. Input text is not echoed in errors.

`SpecDoctor.Analyze(ctx, contract)` returns advisory data with no PASS/approval flag:

- Method: `english-marker-ratio.v1`, producer version `1.0.0`.
- Score: ceiling of `100 * flagged statements / examined statements`.
- Examined statements: goal and every prose declaration. Component identifiers
  are excluded from linguistic scoring.
- A statement is flagged when a case-insensitive Unicode letter token equals
  `fast`, `quick`, `easy`, `simple`, `secure`, `robust`, `appropriate`, `reasonable`,
  `etc`, `maybe`, `should`, `might`, `tbd` or `todo`.
- Suggestions identify the field/assertion and ask for observable, measurable
  criteria. Missing acceptance criteria gets a separate suggestion and does not
  inflate the marker score. Each result owns its slices.

This is an explainable English heuristic, not semantic ambiguity probability,
multilingual analysis or a completeness guarantee. A score of zero does not establish
clarity. Suggestions are review prompts; they do not invent business values, rewrite
the contract, create evidence or authorize production. The original bytes and hash
are preserved.

## SpecForge executable format

SpecForge generates portable **declarative evidence checks**, evaluated with the
existing policy engine. It does not generate arbitrary Go/shell/SQL source from prose.
Verification producers continue to execute their checks through the secure Runner;
this package consumes their normalized, authenticated evidence.

`Assertions(contract)` enumerates every item in all eleven list fields, including
allowed/forbidden components and every constraint family. Goal is preserved as
context. Each assertion ID is:

```text
SHA256(UTF8("safepatch.assertion.v1\n" + field_name + "\n" + canonical_statement))
```

IDs survive list reordering and unrelated additions but change when the declaration
changes. Component semantics remain exact identifiers; generation does not infer
filesystem ancestry, globs or symlink confinement. A binding must select a reviewed
test that actually establishes the declared component restriction or behavior.

Trusted binding configuration maps assertion IDs to a producer, evidence type and
optional minimum confidence. Generation always requires deterministic evidence;
`deterministic: false` cannot relax that requirement and canonicalizes to true.
At least one valid binding is required. Unknown IDs, duplicate bindings, invalid
selectors and more than 128 bindings are rejected. Multiple assertions may share a
check only when a reviewer has established that it verifies each assertion.

A forbidden-behavior check must verify the behavior's **absence**. A failing result
is never inverted into success. Binding a generic passing build to an unrelated
business assertion would be incorrect configuration; SafePatch cannot infer the
semantic validity of a binding from its name.

The immutable `Specification` contains canonical `safepatch.specification.v1` JSON:
schema, generator version, tenant ID, repository ID, full canonical contract and
sorted bindings. Its content hash is SHA256 of the schema, newline and canonical JSON.
`MarshalJSON` returns a copy; `DecodeSpecification` validates bounded JSON and
regenerates rules from the bindings. Unsupported versions, duplicate/unknown/case
alias fields, nulls, injected rules and approval claims are rejected. Direct
`json.Unmarshal` into a Specification is forbidden. The zero value is invalid.

Decoded artifacts and bindings are untrusted configuration until their source and
scope are authorized. Hashes provide integrity identity, not a signature or permission
to import an artifact. Apply advanced management admission before accepting new
uploaded generation configuration. The low-level decoder is not an access boundary.

## Execution and evidence

`Specification.Evaluate(ctx, policy.Input)` executes the generated rules in the shared
policy engine. The tenant and repository must match the artifact. All evidence is
validated against the requested tenant/change/run, creation time, uniqueness and
immutable envelope. Matching observations must additionally contain
`input_hashes["intent_contract"]` equal to the artifact's contract hash. Orchestration
supplies this binding and producer provenance, never scanner text or an LLM.

The existing policy semantics apply: all matching observations must PASS and meet
the confidence/determinism requirements. FAIL or WARN evidence makes a required
check FAIL. Missing, ERROR, SKIPPED, UNAVAILABLE, UNKNOWN, insufficient confidence
or nondeterministic observations cannot satisfy it. Wrong scope, invalid envelopes
or missing/mismatched contract input hashes return ERROR and an error.

Every unbound declaration remains in the artifact and is listed in
`unbound_assertions`. Unbound declarations prevent overall PASS even if the bound
checks pass; an explicit failed check still takes precedence as FAIL. Consumers must
use the top-level evaluation status, not a passing subset in `checks`. This diagnostic
result does not directly authorize production.

Previously generated specifications retain their meaning after an entitlement
downgrade. Pure evaluation does not regenerate or remove mandatory checks. Storage,
retrieval, imported configuration and hosted execution still require independent
authorization and their applicable capabilities/quotas.

## Shared-core example

Inside an already admitted operation (application callers should normally use
SpecService rather than invoke generation directly):

```go
contract, err := intent.NewIntentContract(intent.IntentContractFields{
    Goal: "Keep retries idempotent",
    AcceptanceCriteria: []string{"A retry returns the original receipt"},
})
if err != nil { return err }
assertions, err := spec.Assertions(contract)
if err != nil { return err }
artifact, err := (spec.SpecForge{}).Generate(ctx, contract, authorizedScope,
    []spec.Binding{{
        AssertionID: assertions[0].ID,
        Evidence: policy.EvidenceRequirement{
            Producer: "receipt-test", Type: "receipt.retry", MinConfidence: 0.9,
        },
    }})
if err != nil { return err }
raw, err := artifact.MarshalJSON()
if err != nil { return err }
// Store raw using the existing authorized immutable artifact store.
restored, err := spec.DecodeSpecification(ctx, raw)
if err != nil { return err }
result, err := restored.Evaluate(ctx, trustedRunInput)
// Handle err and result.Status; never turn UNKNOWN into PASS.
```

`trustedRunInput` is an existing `policy.Input` with authenticated scope and immutable
Evidence. Matching evidence needs `Deterministic: true`, qualified confidence, and
the `intent_contract` input hash described above. No subprocess or network request
is performed by the example's generation or evaluation steps.

## Limits, tests and scope

Shared safety bounds reuse IntentContract's 256 KiB / 2,048-item limits, policy's
128-rule / 256-evidence / 16 MiB input limits and a 512 KiB specification envelope
limit. Larger contracts may generate partial specifications with at most 128 bound
assertions; all unbound items remain UNKNOWN. These are resource safety bounds,
independent of commercial quotas. Input loops honor cancellation.

Tests cover basic conflicts and malformed input, reproducible scores, literal
hostile text, immutable artifacts, strict decoding/fuzz seeds, all evidence statuses,
incomplete coverage, confidence/determinism, conflicting observations, tenant/run/
repository/hash isolation, concurrent evaluation, quota/provider failures and secret
redaction. Application tests exercise all three fixed keys for all five edition
labels, real offline Community ownership, independent authorization, worker dispatch
rechecks and unchanged contract/artifact hashes after revocation/restoration.
Integration tests persist, reload and execute artifacts through the existing local
immutable store and enforce tenant/repository/revision isolation.

No database migration, API/CLI command, cloud worker deployment, LLM integration,
natural-language test synthesis or new grant assignment is introduced. Native
PostgreSQL migration checks retain their existing opt-in environment requirement.
