# ProofDependencyGraph and LivingProof

`internal/proof/living` implements one immutable dependency graph and one
reevaluation engine. `LivingProofService` uses the existing execution guard with
`proof.living`, independent `proof.execute` authorization, tenant resolution and
quota admission. There is no edition branch, build flag or separate engine.

The existing catalog identity is `feat_0013` / `proof.living`. The initial and
v0.2 commercial bundles already allocate it to Enterprise only. This milestone
preserves those immutable records. A later Business offering needs a newly
published entitlement configuration/grant, not different LivingProof code.
The historical v0.2 FUTURE presentation metadata is not silently rewritten;
production worker composition remains subject to the v1.0 readiness hold.

## Graph and evaluation semantics

`ProofDependencyGraph` binds an opaque graph ID to the existing `domain.Change`
(organization, repository, base commit and head commit). Input nodes pin immutable
artifact digests; proof nodes pin completed proof digests and original verification
results. Edges point from a proof to its prerequisites, including other proofs.
Node expiry is explicit trusted policy, not a repository-provided timeout.

`NewGraph` validates and seals canonical JSON, sorts nodes/dependencies, and rejects
cycles, missing endpoints, duplicates, invalid digests/IDs, invalid dates and proof
nodes with no prerequisites. Graphs must contain a proof. They have at most 1024
nodes, 4096 edges and 1 MiB of canonical JSON. Topological traversal is iterative.
The manifest describes an explicit dependency set; it cannot prove completeness of
dependencies omitted by its producer.

`FromPassport` reuses the existing Passport without changing its schema. It pins
commit (SHA-256 of the ASCII commit SHA), Intent, ProofPlan, policy and policy-result
digests, EvidenceGraph and any present artifact/SBOM/provenance references. The
`proof` node pins the exact Passport digest. Unknown proof coverage or unknown
verification/policy results stay UNKNOWN. Optional missing references are not
invented. This helper is for authoritative completed Passports or externally
verified organization-trusted statements; parsing a Passport does not authenticate it.

For reevaluation, a trusted reader produces a consistent `Snapshot` bound to the
graph's digest. It must read current retained dependency state, including proof
availability/revocation. Observations carry only opaque IDs, availability and
digests. A snapshot is accepted only within its explicit validity interval, at most
60 seconds from observation; it cannot predate the graph or come from the future.
Unknown/duplicate observation IDs and malformed digests fail validation. An omitted
or unavailable dependency is UNKNOWN, never implicitly unchanged.

The engine propagates dependency state transitively:

| State | Meaning |
| --- | --- |
| CURRENT | Recorded proof passed, its bytes and every declared dependency still match, and all are available and unexpired. |
| STALE | At least one pinned digest changed. |
| EXPIRED | A proof or dependency reached its validity deadline. |
| FAILED | Original verification failed, or a required proof failed. |
| UNKNOWN | Required evidence/observations are unavailable or original verification is unknown. |

When several conditions apply, UNKNOWN takes precedence over FAILED, EXPIRED,
STALE and CURRENT, in that order. Reasons retain each immediate blocking cause.
Unaffected branches remain current. `needs_reverification` deterministically lists
affected proof IDs; it does not enqueue work or approve a deployment. A returned
CURRENT result is never PASS for a new change, a new policy or production admission.
The caller must continue using the existing verification/policy/trust guards.

`LivingProof` includes the graph digest, observation snapshot, evaluation time,
bounded validity deadline, per-proof states/reasons and re-verification IDs.
Its validity cannot exceed the snapshot or the next node expiry. Graph/evaluation
values own canonical bytes; callers receive defensive copies. Reevaluating a
change never overwrites an earlier proof. Reverting to identical retained inputs
can make an unexpired original proof current again; this is content-based freshness,
not proof of an intervening deployment's safety.

## Service and persistence

Trusted composition creates `NewLivingProofService(tenants, grants, permissions,
quotas, source, archive, clock)` and calls
`Reevaluate(ctx, authenticatedActor, repositoryScope, graphID)` after authenticating
the actor or loading canonical job provenance. The source receives only an admitted
scope and opaque ID. It must load the server-owned graph and current observations,
check evidence retention, and never treat scanner/LLM/client claims as authority.
The service verifies the returned organization, repository, graph ID and snapshot
binding, reevaluates, rechecks access before archival and again before returning.
It rejects a result whose validity elapsed during a slow write. Quota admission
happens once; access rechecks do not reserve additional quota.

`internal/platform/livingstore.Archive` reuses `evidence.OriginalStore`, including
the existing private S3-compatible and restricted local implementations. Graphs
and evaluations are separate digest-addressed immutable objects in the existing
organization/repository/head-commit namespace. No new database schema or migration
is needed. A partial write can leave an immutable graph, but no successful
evaluation receipt is returned until both writes succeed and receipts match bytes.
Retention/storage admission and immutable object lifecycle remain caller concerns.

Archive reads require an admitted scope and exact graph/result references. They
check byte counts, digests and embedded scope, then recompute the result against
the stored graph/snapshot to reject altered state fields. Reads return historical
evidence, not current freshness; use the guarded service and a new authoritative
snapshot for current state. Structural digest validation is not cryptographic
issuer authentication. Lower-level graph/codec/archive APIs do not grant access
and must not be exposed directly as ungated hosted endpoints.

The shared OpenAPI document contains the graph, snapshot and LivingProof artifact
schemas. This milestone does not advertise an HTTP route that lacks composition.

## Validation and boundaries

Tests cover transitive invalidation and unaffected branches; missing, unknown,
failed and expired inputs; snapshot binding/freshness; cycles and malformed JSON;
result poisoning; immutable copies and decoding; Passport reference reuse; quota,
tenant, source/storage errors and cancellation; downgrade during a source read;
expiry during storage; all five editions with real entitlement resolution and
PermissionService; 25 foreign-organization combinations; and real local archive
round trips, denied downgrade access and byte-identical history/restoration.

The integration matrix derives the initial allocation from the existing edition
configuration, then grants `proof.living` to each edition to prove that functionality
changes through the entitlement ledger alone. No premium operation bypasses an
actor permission check. Existing migration/edition contract tests remain applicable
because no SQL or historical bundle changed.

Limits of this milestone: one repository/change per graph; explicit dependencies
rather than automatic organization-wide discovery; no event subscription, scheduler,
NATS consumer, HTTP route or automatic re-verification dispatch; no concrete
production current-dependency reader. A worker can call the guarded service using
the same core, but must supply authoritative dependency observations and run affected
proofs through the existing verifier. No live S3/PostgreSQL deployment was added.
The v1.0 NO-GO decision remains in force.

Validation on 2026-09-07: focused LivingProof/archive tests, repository architecture
checks, `go test ./...`, `go test -race ./...` and `go vet ./...` passed. A five-second
graph-decoder fuzz smoke passed with only three executions; that small sample is
not evidence of broad fuzz coverage. Existing opt-in native PostgreSQL/scanner/
container checks were not enabled; no new migration needed native execution.
