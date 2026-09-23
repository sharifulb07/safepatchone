# Canonical IntentContract

`internal/intent.IntentContract` is the one intent model shared by Community,
Developer, Team, Business and Enterprise. Every edition and intent capability uses
the same fields, validation, canonical JSON and content hash. The core package has
no dependency on commercial metadata, entitlement services or actor permissions;
architecture tests enforce that boundary.

## Fields

The wire format contains exactly twelve fields, with no edition or tier metadata:

| Field | Type | Meaning |
| --- | --- | --- |
| `goal` | string | Intended outcome of the software change |
| `requirements` | string array | Declared requirements |
| `acceptance_criteria` | string array | Conditions for accepting the change |
| `required_behaviors` | string array | Behaviors the change must preserve or introduce |
| `forbidden_behaviors` | string array | Behaviors the change must not introduce |
| `allowed_components` | string array | Explicit repository-relative component identifiers |
| `forbidden_components` | string array | Explicit repository-relative component identifiers |
| `protected_properties` | string array | Properties that must remain intact |
| `security_constraints` | string array | Security requirements |
| `database_constraints` | string array | Database/schema/data requirements |
| `api_constraints` | string array | Interface and compatibility requirements |
| `business_constraints` | string array | Business behavior requirements |

All list items are literal data. The model does not execute SQL, commands,
expressions, URLs, scanner text or prompts. Acceptance criteria and constraints
are declarations, not evidence that those conditions were met. There is no status
or approval field. Creating or decoding a valid contract cannot establish PASS or
authorize production, including when an LLM supplied its text.

The goal is required and nonblank. All lists may be empty: an empty list means
no declarations were supplied in that field, not an authorization or proof that
no constraints apply. Basic intent operations retain every field, including
security, database, API and business constraints.

## Construction and serialization

```go
contract, err := intent.NewIntentContract(intent.IntentContractFields{
    Goal: "Keep checkout retries safe",
    Requirements: []string{"Do not charge a retry twice"},
    AcceptanceCriteria: []string{"Retries return the original receipt"},
    AllowedComponents: []string{"internal/payments", "api/checkout"},
    ForbiddenComponents: []string{"internal/auth"},
    DatabaseConstraints: []string{"Preserve historical receipts"},
    BusinessConstraints: []string{"At most one charge per request identity"},
})
if err != nil { return err }
raw, err := contract.CanonicalJSON()
// Handle err and authorize storage before storing raw.
restored, err := intent.DecodeIntentContract(raw)
fields, err := restored.Fields()
hash, err := restored.ContentHash()
// Handle errors; fields and raw are independent copies.
```

`IntentContractFields` is a mutable construction/inspection DTO.
`IntentContract` keeps private canonical bytes and has no mutators. Modifying
constructor slices, returned fields or JSON does not alter it. Shared reads are
safe across goroutines. The zero value is invalid. `json.Marshal` works;
`json.Unmarshal` into a contract deliberately returns `ErrImmutableContract`.

Construction converts CRLF in prose to LF, preserving other whitespace, case and
Unicode text. LF and tab are accepted in prose; terminal controls, lone CR, NUL,
invalid UTF-8 and invisible format controls are rejected. Statements are not
translated, semantically rewritten, or interpreted as executable policy.

Each list is an unordered declaration set: items are sorted by their exact UTF-8
string value and exact duplicates are removed. Input order carries no priority.
Constructor nil lists become explicit `[]`. JSON field order is fixed by the shared
DTO. No Unicode normalization or case folding is applied.

`DecodeIntentContract` requires all twelve fields, including explicit empty arrays.
It rejects missing fields, null fields/items, duplicate keys, case aliases, unknown
fields, wrong types, trailing documents, malformed Unicode and excessive nesting,
reusing the existing bounded JSON validator. It does not silently drop constraints
or upgrade schemas. Errors use `ErrInvalidContract`, `ErrConflictingContract` and
`ErrImmutableContract`; supplied text is not echoed in those messages.

The content hash is:

```text
SHA256(UTF8("safepatch.intent.v1\n") || canonical JSON)
```

`ContractSchema` is a versioned hash/format contract, independent of a software
release or edition bundle. It is not an additional wire field. All twelve fields
are bound to the hash. Canonically equivalent sets have the same hash. A hash is
an integrity identifier, not a signature, tenant identity or proof of provenance.

## Components, contradictions and bounds

Components are exact, case-sensitive repository-relative identifiers such as
`payments` or `internal/payments`. They are not URLs, absolute paths, platform paths
or traversal expressions. Validation reuses the existing safe relative-path rules;
it rejects `..`, `.` segments, repeated separators, backslashes, colons and leading
or trailing whitespace. No glob, prefix or filesystem matching is performed.

The model rejects an exact component present in both allowed and forbidden sets,
and an exact normalized behavior present in both required and forbidden sets.
It does not infer natural-language contradictions, directory ancestry, filesystem
case equivalence or symlink targets. Future consumers must resolve components
against trusted repository metadata and retain Runner/filesystem confinement.
Validation performs no filesystem access and therefore cannot certify a symlink.

Safety bounds are 256 KiB canonical/input JSON, 8 KiB for the goal, 4 KiB per prose
statement, 512 bytes per component, 256 items per list and 2,048 total input items.
Input bounds apply before deduplication. The final encoded-size check accounts for
JSON escaping. These are shared resource safety limits, not commercial quotas.

## Capabilities and trusted ownership

`application.IntentRequirement(catalog, capability)` accepts only the existing
`intent.basic`, `intent.advanced` and `intent.specforge` keys. It returns that
capability plus the existing `proof.execute` permission for intent work associated
with a verification/change. It neither grants ownership nor implements the work.

Trusted composition selects the operation capability and passes the requirement
to `ExecutionGuard`, API/CLI guard helpers or `WorkerOperation`. Workers recheck
current tenant ownership, entitlement, independent actor permission and quota
before dispatch. Repository content, prompts and queue claims must never choose a
cheaper capability for an advanced operation. Catalog membership alone is not
implementation readiness. No existing grants or edition matrices change here.

The model itself is portable and contains no tenant/change/run identifiers. Trusted
orchestration must bind it to an authorized tenant, repository and change/run in
storage or an outer envelope. Identical contracts in different tenants can have
identical hashes; that does not permit cross-tenant reads. Canonical bytes work
with the existing scoped immutable artifact store. New content creates a new
content-addressed artifact and preserves previous versions; no contract-ID registry
or assignment lifecycle is introduced.

Entitlement changes restrict advanced operations without changing the stored
contract or deleting fields. Reading/validating an existing full schema is not
itself permission to analyze it, generate specifications or perform production
actions. No CommunityIntent, EnterpriseIntent or edition-specific parser exists.

## Validation and milestone scope

Tests cover every field, exact schema, canonical ordering, all mutable aliases,
content hashes, Unicode/control characters, contradictory declarations, path and
resource bounds, strict JSON, concurrent reads and parser fuzzing. Integration
tests use the existing immutable store with tenant/repository/revision isolation.
Worker tests exercise all three capability keys across all five edition labels,
including ownership denial, permission denial after entitlement, dispatch rechecks,
cancellation and unchanged contract bytes after revocation/restoration.

The contract model adds no database migration, API/CLI route or production approval
workflow. Shared [SpecDoctor and SpecForge](specdoctor-specforge.md) now consume this
same model for explicit validation, advisory analysis and bound declarative evidence
checks. They do not infer natural-language completeness or change stored fields.
Existing migration regressions remain in the full Go suite; native PostgreSQL tests
still require their existing opt-in environment.
