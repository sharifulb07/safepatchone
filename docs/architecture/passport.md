# SafePatch Passport

`safepatch.passport.v1` is one immutable JSON format for Community, Developer,
Team, Business and Enterprise. It contains no edition, subscription, retention,
license, or deployment-channel fields. Its schema version is independent of the
product release. The contract is also in the single
[OpenAPI specification](../../api/openapi/openapi.yaml).

The shared `ChangeWorkflow.Run` and `RunHosted` create the same unsigned Passport
after verification and policy evaluation. `ChangeArchive` preserves it alongside
Intent, SIR, ChangeGraph, ProofPlan, evidence, the EvidenceGraph manifest and the
policy result. There is no additional verification engine or edition branch.

| Payload field | Meaning |
| --- | --- |
| `change`, `change_id`, `run_id` | Organization, repository, base/head commits and exact run binding; ChangeID is recomputed on decode. |
| `intent_digest` | Existing sealed IntentContract content hash. |
| `commit` | Exact head commit; must equal `change.head_sha`. |
| `artifact_digest` | SHA-256 of build artifact bytes, or explicit `null` if unavailable. This is not the proof archive digest. |
| `sbom`, `provenance` | `{digest, media_type}` references to immutable bytes in the subject's authorized store, or explicit `null`. |
| `proof_level` | Achieved L0–L8, or `UNKNOWN`. A requested ProofPlan target alone cannot establish it. |
| `proof_plan_digest` | Existing ProofPlan hash, including its configured obligations. |
| `policy_decision` | Policy hash, SHA-256 of the serialized policy result, and its exact status. |
| `evidence_graph_digest` | Domain-separated digest of the archived run-scoped EvidenceGraph manifest. |
| `verification_decision` | Existing diagnostic PASS, FAIL or UNKNOWN, never production authorization. |
| `issued_at` | Trusted workflow clock in UTC. |

Absent build output, SBOM and provenance remain null in **all** editions. This
milestone references these objects; it does not generate them, fetch remote URLs,
validate their standards compliance, or attest that they were built from the
commit. The source adapter must resolve their digests from actual immutable
bytes. Consumers that require those attestations must verify the referenced
bytes and their binding to the subject with their own trusted policy.

The workflow records a known achieved level only when its ready plan's required
observations pass and SIR coverage is complete. Missing, failed or unavailable
observations cannot become a known achieved level. Signing UNKNOWN authenticates
the UNKNOWN statement; it does not promote it to PASS. Any LLM-produced Intent
or scanner payload remains an inert assertion. Neither this Passport nor an LLM
authorizes production.

## EvidenceGraph manifest

`graph.NewEvidenceManifest` accepts a bounded explicit subgraph from one run.
It pins evidence IDs to their existing immutable evidence hashes and includes
directed relationships. Tenant, ChangeID and run must match on every node.
Duplicate identities, foreign tenants, dangling edges, and oversized inputs
fail. Nodes sort by evidence ID and edges by edge ID; timestamps use the existing
edge canonicalization. Empty graphs remain representable for incomplete runs.

The JSON fields, in order, are `schema`, `change`, `change_id`, `run_id`, `nodes`,
`edges`. Schema is `safepatch.evidencegraph.manifest.v1`. Nodes contain
`evidence_id`, `digest`. Edges use the existing `graph.Edge` JSON format. Its
digest is lowercase SHA-256 of schema + newline + canonical JSON. Reconstruct it
from the archived evidence and explicit edges to verify the digest. The workflow
currently supplies its verification evidence and no relationship assertions.
This is a run-scoped subgraph, not a claim of organization graph completeness,
and is distinct from the source-oriented ChangeGraph.

## Encoding, integrity and signing

Use `passport.New`, `passport.Decode`, `Passport.MarshalJSON`, `Passport.Fields`
and `Passport.Digest`. Sealed objects own their bytes and return independent
copies. Decode rejects extra or missing keys, case aliases, duplicate keys at
any depth, invalid digests, inconsistent ChangeIDs/commits, invalid Unicode,
invalid status/level combinations and inputs over 16 KiB. No paths are opened,
commands executed, or network requests issued by Passport code.

The envelope is `{schema, payload, signature}` in that order. An unsigned
Passport has `signature: null`. Payload fields use the table order above
(`change`, `change_id`, `run_id` are three individual keys). Canonical JSON is
compact `encoding/json` output from the published Go structs, with no trailing
newline; keys remain in struct declaration order, arrays retain their defined
order and timestamps use UTC RFC3339Nano. The input objects here use restricted
ASCII strings. Whitespace and object key order can vary on decode, but decode
returns canonical bytes. This is SafePatch's versioned encoding, not a claim of
JCS, DSSE, in-toto or SLSA compatibility.

Signatures use Ed25519 over:

```text
safepatch.passport.v1\nEd25519\n<key_id>\n<canonical payload JSON>
```

Here `\n` denotes one LF byte. The signature object contains `algorithm`,
`key_id`, `value`, where value is standard padded base64. `Passport.Digest()` is
SHA-256 of the entire canonical envelope, including the signature; unsigned and
signed variants therefore have different file digests. Signing retries with the
same statement/key are deterministic. Existing signatures are never silently
replaced. New signing creates a new immutable object.

`passport.Verify` requires an externally trusted organization ID, key ID and
public key. Passport data cannot nominate its own public key or discovery URL.
Organizations can rotate or revoke accepted keys in external trust policy
without rewriting historical Passports. Verification proves signature integrity
and the expected organization, not freshness, reference availability, issuer
competence, or current authorization. Consumers must match the expected
repository/commit/run and apply trusted freshness and acceptance requirements.

## Hosted boundaries and storage

`HostedPassportService.Sign` accepts an authenticated actor, organization/repository
scope and completed run ID. It resolves current ownership, requires
`EntitlementService.Has(proof.passport)`, separately checks
`PermissionService.Allowed(proof.execute)`, and invokes the existing quota hook.
It loads the Passport through `CompletedPassportSource`, verifies scope/run,
then refreshes access before signing without reserving quota twice. A signer
cannot rewrite the payload. Backend error strings are redacted.

`CompletedPassportSource` is a trusted composition port for **completed run
state**, not an upload endpoint or arbitrary object-digest lookup. Its adapter
must establish authoritative completion before returning a Passport. Never wire
an uploaded JSON/blob reader directly into this port. `Ed25519Signer` provides a
concrete signer with a copied, organization-bound deployment key. A KMS/HSM
adapter can implement the same signing port without changing the format.

The existing organization entitlement bundles are unchanged. `proof.passport`
governs hosted signing here; constructing, exporting, decoding and locally
signing the portable format do not require that grant. This clarifies older
catalog references to Passport availability: they do not prohibit portable
local serialization. Subscription/trial changes deny subsequent hosted signing
without corrupting existing documents or changing their cryptographic validity.

`passportstore.Archive` reuses `evidence.OriginalStore`; the existing local adapter
provides immutable storage and reopen/read verification. Storage validates the
returned digest/byte count and subject scope. An archive read alone establishes
no trusted completion and is not a signing authorization. Hosted storage must
apply `proof.cloud_history`, current `retention_days` quota/admission and access
permissions using existing services. Organization-managed acceptance policy
uses the existing `policy.central` and `policy.manage` boundaries. Those settings
are outside the signed schema; this milestone adds no retention scheduler or
organization trust-policy management endpoint.

## Validation and deployment limits

Tests cover canonical round trips, explicit unknowns, signature trust and
tampering, malformed/ambiguous input, graph ordering and tenant/run boundaries,
local durable storage, all five editions, independent authorization, entitlement
revocation during source reads and unchanged historical signatures after a
downgrade. Shared workflow integration tests bind Passport fields to actual
Intent, ProofPlan, policy and graph outputs and preserve UNKNOWN on incomplete
runs. A fuzz target covers decoding.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/proof/passport -run '^$' -fuzz FuzzDecode -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. The bounded fuzz run passed with 174 executions and no
failure. The existing GitHub PR migration contract passed; native PostgreSQL
execution skipped because `SAFEPATCH_POSTGRES_MIGRATION_TEST=1` and libpq
connection settings were not configured. No dependencies were added.

No SQL migration or database schema change is required: Passports are immutable
artifact bytes. The existing migration history and its tests remain shared.
There is no new HTTP signing/download route, CLI command, production key setup,
KMS integration, or production completed-run source adapter in this milestone.
The shared workflow and archive integration are runnable Go components; hosted
deployment still needs the already documented source/extractor, sandbox, object
storage and completed-run composition. Do not equate the test suite with a live
GitHub/S3/PostgreSQL/KMS validation.

## Change inventory

Inspected: evidence sealing/original-store contracts; ChangeID; Intent; SIR;
ProofPlanner; policy and trust decisions; EvidenceGraph and ChangeGraph; shared
ChangeWorkflow/ChangeArchive; execution guards; entitlement and quota services;
feature/edition catalog; local storage; GitHub core tests; architecture rules;
OpenAPI and migration-test layout.

Created: `internal/proof/passport/passport.go` and its tests;
`internal/graph/evidence_manifest.go` and its tests;
`internal/application/passport_service.go` and its tests;
`internal/platform/passportstore/archive.go` and its tests; this document.

Modified: `internal/application/change_workflow.go`,
`internal/application/execution_guard.go`,
`internal/platform/githubapp/core_integration_test.go`,
`api/openapi/openapi.yaml`.

Next milestone prompt: "Wire Passport signing and authorized download into the
hosted completed-run composition, with an authoritative completed-run source,
organization key rotation, and existing retention/authorization enforcement."
