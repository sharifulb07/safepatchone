# Immutable Evidence

`internal/evidence.Evidence` is the shared sealed evidence value. Community,
Developer, Team, Business and Enterprise use precisely the same schema and
hashing rules. No edition, subscription, capability grant or production approval
is embedded in Evidence.

## Fields

| JSON field | Meaning |
| --- | --- |
| `evidence_id` | Producer/orchestration-assigned immutable evidence identity |
| `tenant_id` | Authoritatively resolved tenant namespace |
| `change_id` | Trusted change identity |
| `run_id` | Trusted execution/run identity |
| `producer` | Stable producer identifier |
| `producer_version` | Explicit producer implementation version |
| `category` | Lowercase dotted classification key, e.g. `verification` |
| `type` | Lowercase dotted evidence type, e.g. `go.test` |
| `deterministic` | Explicit producer assessment; never inferred from PASS |
| `status` | Existing PASS, FAIL, WARN, ERROR, SKIPPED, UNAVAILABLE or UNKNOWN |
| `severity` | Existing INFO, WARNING or ERROR |
| `confidence` | Finite number in [0, 1]; zero asserts no confidence |
| `summary` | Bounded plain text; no terminal controls |
| `payload` | Bounded untrusted JSON object, not executable instructions |
| `source_locations` | Repository-relative paths and optional one-based inclusive ranges |
| `artifacts` | Logical artifact ID, SHA-256 content hash and nonnegative byte size |
| `input_hashes` | Logical input identifier to lowercase SHA-256 map |
| `content_hash` | Computed domain-separated SHA-256 integrity hash |
| `created_at` | Caller-supplied timestamp normalized to UTC, retaining nanoseconds |

Identifiers are bounded portable tokens. Source locations have `path`,
`start_line`, `start_column`, `end_line`, `end_column`; zero positions denote an
unspecified coordinate or whole file. Invalid, reversed or inconsistent ranges
are rejected. Paths cannot be absolute, URLs, traversals or Windows paths.
Artifacts contain `artifact_id`, `content_hash`, `size_bytes`; references have no
URL or host-path field and inherit the Evidence tenant's resolution scope. Artifact
bytes remain external and may exceed the process-capture size ceiling.

PASS requires INFO severity; WARN cannot carry ERROR severity. Confidence,
determinism, artifact presence and hash validity never promote UNKNOWN or any
other status into PASS. An evidence value is not a production authorization.

## Construction and immutability

```go
sealed, err := evidence.NewEvidence(evidence.EvidenceFields{
    EvidenceID: "ev_123", TenantID: "tenant_123",
    ChangeID: "change_123", RunID: "run_123",
    Producer: "go", ProducerVersion: "1.0.0",
    Category: "verification", Type: "go.test",
    Deterministic: false, Status: evidence.StatusUnknown,
    Severity: evidence.SeverityInfo, Confidence: 0,
    Summary: "Verification has not established an outcome.",
    Payload: json.RawMessage(`{"reason":"incomplete"}`),
    CreatedAt: runTimestamp,
})
// Handle err. runTimestamp and identities come from trusted orchestration.
canonical, err := sealed.CanonicalJSON()
// Store only after tenant/actor/retention/quota admission.
restored, err := evidence.DecodeEvidence(canonical)
snapshot, err := restored.Fields()
// snapshot is an independent DTO; modifying it does not modify restored.
```

`EvidenceFields` is a mutable construction/inspection DTO. `Evidence` stores only
private canonical bytes, has no setters, and copies all data on output. Constructor
inputs, payloads, maps, slices and returned JSON cannot mutate a sealed value.
Readers can share a sealed value concurrently. Reassigning a Go variable is still
possible; the API does not permit modifying the object it previously referenced.

`NewEvidence` computes the hash and rejects a supplied `content_hash`. Use
`DecodeEvidence` to restore a complete serialized value and verify its hash.
`json.Marshal` works; `json.Unmarshal` into Evidence deliberately returns
`ErrImmutableEvidence` instead of overwriting it. The zero value is invalid and
cannot marshal as apparently valid evidence.

Decoding requires all 19 top-level fields and complete typed nested references.
Duplicate keys, case aliases of schema keys, unknown schema fields, explicit nulls,
trailing documents, invalid Unicode and over-deep JSON fail closed. Payload keys
are arbitrary data, not Evidence schema fields. No schema upgrades or heuristic
field repairs occur during decode.

## Canonicalization and integrity

The domain tag is `safepatch.evidence.v1`; it is not an extra edition-dependent
wire field. `content_hash` is SHA-256 of:

```text
UTF8("safepatch.evidence.v1\n") || canonical JSON with content_hash set to ""
```

All fields, including tenant/change/run/evidence identity and creation time, are
bound by the hash. Equal payloads belonging to different tenants or runs do not
have the same Evidence hash. The final wire representation includes the computed
hash; its ordinary blob SHA-256 is different from `content_hash`.

Canonicalization uses fixed top-level field order, recursive JSON object-key
ordering, sorted input hash keys, UTC time, positive numeric zero for confidence,
sorted/deduplicated source locations, and artifacts sorted by unique artifact ID.
Constructor nil payload becomes `{}`; nil collections become `[]` or `{}`. Payload
array order is preserved. JSON numbers retain their lexemes and precision:
`1`, `1.0` and `1e0` intentionally hash differently. This is SafePatch's versioned
JSON contract, not a claim of RFC 8785 canonicalization.

The serialized envelope is bounded to 1 MiB, payload to 512 KiB, source locations
to 1,024, artifacts to 256 and input hashes to 256. Excess data is rejected, never
silently truncated into successful evidence; large reports belong in artifacts.
Repeated artifact IDs are rejected rather than ambiguously deduplicated.

A content hash detects corruption, not forgery. An attacker able to replace both
the envelope and its hash can create another hash-valid envelope. Authentication,
tenant ownership, trusted run provenance and any future signatures remain separate
requirements. Payload data may contain sensitive material: keep it out of logs
and escape/redact it at authorized presentation boundaries.

## Reusing verifier evidence and storage

`FromVerification(record, VerificationIdentity)` validates the existing SDK Record,
retains it as the payload, copies its status and producer identity, derives source
locations and severity, and seals it with caller-supplied run/tenant identity,
confidence, determinism, artifacts and input hashes. All seven statuses remain
unchanged. It does not invent IDs or confidence from scanner output.

The existing `safepatch.verification.v1` Record remains the verifier normalization
contract. Its wire format and the SDK lifecycle are unchanged. Callers explicitly
convert after trusted orchestration knows the tenant, change and run. Evidence
construction does not automatically execute, admit or publish a verification.

Canonical Evidence bytes can use the existing scoped `OriginalStore` and local
immutable storage implementation. Original scanner output is retained separately,
without redaction or byte changes. Existing artifacts are referenced, not fetched
or extracted by this model. Storage callers must bind `tenant_id` and artifact IDs
to an independently authorized scope; syntactically valid identities are not proof
of tenant membership.

The subsequent [EvidenceGraph milestone](evidence-graph.md) adds PostgreSQL
persistence with unique `(tenant_id, evidence_id)` identities, immutable repository
bindings and rejection of replacement content. The content-addressed blob store
itself still enforces immutable bytes rather than Evidence-ID uniqueness. Trusted
orchestration must bind each change/run/artifact to its tenant. Corrections produce
new Evidence IDs and preserve previous records; no signing system is introduced.

## Tests and entitlement effects

Unit/security tests cover all fields, round trips, canonical ordering, precise
numbers, tenant/run tampering, invalid paths/hashes/ranges, bounded payloads,
malformed JSON, defensive copies, concurrent reads, strict decoding and every
status. A parser fuzz target is included. Integration tests round-trip sealed
evidence through the existing immutable local store, reject cross-tenant reads,
and preserve previous evidence and original output when new evidence is added.

All five editions use identical serialized fixtures. No premium feature, grant,
quota or edition bundle changes; existing entitlement and migration regressions
remain part of `go test ./...` and `go test -race ./...`. This extends the shared
foundation after the v0.1 preparation record; it does not retroactively change
that release's feature matrix.
