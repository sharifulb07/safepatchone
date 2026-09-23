# Tenant-boundary hardening

Tenant identity is independent of edition. Admission still follows authenticated
principal, organization/resource resolution, current entitlement, permission,
quota, then operation. Enterprise membership does not authorize another
Enterprise organization. Community membership does not authorize Team resources.
No feature keys, edition bundles, subscription state, Passport schema or
verification engine changed in this milestone.

| Boundary | Enforcement and adversarial coverage |
| --- | --- |
| PostgreSQL | Organization membership and repository SQL predicates; composite project/repository, Replay and MCP foreign keys; rejected requests preserve victim rows. |
| NATS | Exact organization subject/envelope match, strict bounded JSON, organization-scoped deduplication IDs, scoped durable job lookups; injected actor/edition claims rejected. |
| S3 | Scope-bound evidence adapter constructs tenant/repository/revision keys; immutable conditional writes and bounded digest-checked reads; identical bytes do not share tenant object keys. |
| Cache | Organization-keyed snapshots, current-state validation, immutable returned copies; concurrent same-ID lookups and foreign-state poisoning cannot share grants. |
| Replay | Dataset IDs and digests resolve within organization/repository; foreign reads, claims and dispatch rejected before execution. |
| ChangeGraph | Scoped SQL and canonical-node identity checks reject foreign rows even when entity/repository/revision IDs collide. |
| GitHub | Verified webhook installation/repository pair resolves the tenant; claimed organization/edition is ignored; job claim, completion and Check delivery include organization predicates. |
| MCP | Scope-bound runtime and leases; foreign invocation/revocation denied; multiple Authorization headers rejected. |

The Go matrix tests enumerate all 25 ordered edition pairs, including two distinct
Enterprise organizations. Persistence and transport components intentionally do
not accept an edition as authority; their matrices use edition-labelled tenant
identities. Cache tests use actual edition-bearing entitlement records. Admission
tests grant capabilities independently so commercial denial cannot substitute for
tenant denial. Replay/MCP spies assert foreign tenants never reach entitlement
resolution. Existing premium-feature tests retain positive, negative, permission
and downgrade/data-preservation coverage.

## Queue protocol and deployment

Provision stream subjects `safepatch.tenant.*.proof` and
`safepatch.tenant.*.github_pr`, with separate durable consumers filtered by work
kind. A message contains exactly:

```json
{"organization_id":"org_example","work_id":"durable_work_id"}
```

`Nats-Msg-Id` is `<organization_id>:<kind>:<work_id>`. No actor, edition, evidence,
permissions or grants travel in the envelope. Workers reload authoritative state
and recheck admission. Mismatched subjects, duplicate/unknown JSON keys, oversized
messages, subject wildcard injection and legacy global subjects are terminated.

Authenticated broker permissions are required: only trusted backend publishers
may publish work. If a publisher is scoped to `org_example`, permit only its
exact subjects `safepatch.tenant.org_example.proof` and
`safepatch.tenant.org_example.github_pr`; deny access to other tenants' subjects
and broker administration. Trusted multi-tenant outbox/worker identities need
broader subjects and must not be exposed to customers. Subject validation alone
does not authenticate a publisher who can publish arbitrary broker subjects.
Use TLS, bounded message sizes, delivery budgets and appropriately scoped
JetStream API permissions. These deployment ACLs are not automatically installed.

Coordinate publisher, consumer, stream-filter and permission updates. Drain old
queues before switching; new consumers deliberately reject the old wire format.
For undrained work, reconcile durable database jobs with acknowledged work before
republishing. GitHub's outbox retries unfinished jobs after ten minutes; keep the
broker deduplication window shorter. Onboarding publishes only rows without
`published_at`, so already-published unfinished jobs require an operator-reviewed
republication after reconciliation. Do not clear completed work or erase receipts.
There is no legacy-message compatibility fallback that trusts an unbound tenant.

## Storage and operational boundaries

`evidencestore.NewS3` implements the shared original-evidence store over a trusted
`S3Objects` transport port. Object keys begin with
`organizations/<sha256(organization_id)>/`; the next segment hashes repository
and revision together. The adapter is bound to one authenticated archive scope,
accepts no public URLs, isolates content deduplication by scope, and verifies
stored size/digest after conditional creation and on every read. Keep buckets
private and restrict credentials to the necessary prefixes. Construct scope-bound
adapters only after shared tenant admission, as with the local evidence store.
No S3 SDK transport, credential provisioning or cloud IAM policy is supplied here.

Database credentials belong to trusted backend services. Composite foreign keys
prevent cross-organization references; they do not turn an unrestricted database
connection into a tenant-sandboxed SQL console. SQL adapter tests inspect predicates
and bindings; the existing schema does not implement connection-level RLS.
No migrations are added or rewritten. One native fixture applies migrations
through 000016 and exercises all 25 edition pairs against project, Replay and MCP
constraints, including preserved victim data after failed writes.

## Validation

Run `go test ./...` and `go test -race ./...`. Native constraint execution requires
`SAFEPATCH_POSTGRES_MIGRATION_TEST=1`, `psql` and libpq connection settings, then
`go test ./internal/platform/migrationtest -run TestPostgresTenantBoundaries -v`.
The harness creates and removes only its own temporary schema.

The S3 transport and SQL driver adversarial tests use fixtures. Queue tests use
real NATS message objects and the production codec without a running broker.
Live PostgreSQL constraints, NATS authentication/ACLs, S3 IAM and cloud-provider
interoperability must be verified in a configured deployment; unit/race passes
are not evidence of those infrastructure controls.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...`, and
`go vet ./...` passed. The native PostgreSQL matrix explicitly skipped because
the database opt-in was unset; `psql`, `nats` and `docker` were not on PATH.
No live broker, database or cloud storage claim is made.

## Task file inventory

Inspected existing organizationstore PostgreSQL and entitlement persistence,
tenant/permission/execution guards, SnapshotService, graphstore adapters,
ReplayService and replaystore, AgentGateway and MCP runtime/handler/lease stores,
GitHub admission/webhook/store/worker, onboarding publisher/consumer/proof jobs,
original evidence interfaces/local store, migrations through 000016 and the
migration harness, the shared API specification and architecture documentation.

Created:

- `internal/platform/workqueue/message.go`, `message_test.go`
- `internal/platform/evidencestore/s3.go`, `s3_test.go`
- `internal/entitlement/tenant_matrix_test.go`
- `internal/platform/organizationstore/tenant_matrix_test.go`
- `internal/platform/graphstore/tenant_matrix_test.go`
- `internal/platform/replaystore/tenant_matrix_test.go`
- `internal/platform/mcpgateway/tenant_matrix_test.go`
- `internal/platform/githubapp/tenant_matrix_test.go`
- `internal/platform/migrationtest/tenant_boundaries_test.go`, `tenant_boundaries.sql`
- This document

Modified:

- `internal/platform/githubapp/store.go`, `worker.go`, `jetstream.go`, `webhook.go`
- `internal/platform/onboardingstore/jetstream.go`, `consumer.go`
- `internal/platform/mcpgateway/handler.go`
- `internal/platform/billingstore/github_pr_test.go`
- `docs/architecture/onboarding.md`, `github-app.md`

No schema migration, module dependency, API endpoint, edition fork or commercial
feature gate was added. The main deployment compatibility change is the queue
wire protocol described above. Next validation should provision isolated native
PostgreSQL, authenticated NATS and private S3-compatible storage, implement the
trusted S3 transport, and run the tenant matrix against their actual permissions.
