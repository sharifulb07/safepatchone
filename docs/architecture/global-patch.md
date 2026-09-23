# GlobalPatch and GlobalChangeID

GlobalPatch is an immutable manifest of coordinated repository changes within one
organization. Global means a complete declared set across repositories; it never
crosses tenant boundaries. It does not apply patches, infer undeclared repositories,
prove compatibility or approve deployment.

The shared `internal/graph` implementation reuses `domain.Change` and its ChangeID
algorithm. A manifest pins organization, intent digest, 2-64 distinct repositories,
each repository's base/head commits, and up to 1024 dependency edges. Each edge
states that one ChangeID requires another. Dependencies may be empty, but cannot
be null, duplicate, cyclic, self-referential or refer outside the set. No-op changes,
duplicate repository entries and mixed-organization sets are rejected.

Canonical JSON sorts changes by repository ID and edges by ChangeID then required
ChangeID. It is bounded to 1 MiB. `GlobalChangeID` is `globalchange_` followed by the
hex SHA-256 of `safepatch.global-patch.v1`, a newline, and canonical JSON. Changing
commits, membership, intent, organization or dependencies produces another ID.
Reordering input does not. Timestamps, software version, release channel, edition
and entitlement source are deliberately absent from this identity. Fields returned
by the domain are defensive copies. Strict decoding rejects duplicate/unknown keys,
case aliases, nulls, malformed identifiers and trailing documents.

## Admission and persistence

`application.GlobalPatchService.Save` and `Read` require `graph.global_change`.
Save uses independent `proof.execute` permission; Read uses `proof.view`. Both
resolve and authorize every repository, admit the configured quota once, access
the archive, validate its output and recheck current access before returning.
Each request and worker invocation must supply an authenticated principal. A
licensing or subscription label cannot substitute for an effective grant.

`platform/globalpatchstore.Archive` implements the service port using the existing
`evidence.OriginalStore`, supporting existing local and private S3 adapters without
a second evidence format or database. The first sorted repository anchors storage;
the GlobalChangeID is the revision namespace. Exact bytes are content-addressed and
repeat saves are idempotent. The locator contains organization, sorted complete
repository roster, GlobalChangeID and the existing hash/length reference, never a
URL. Like all existing evidence adapters, the archive is a trusted internal port,
not a substitute for application admission.

Existing Local/S3 handles bind one scope. Trusted composition must construct the
handle for the organization's anchor repository and GlobalChangeID, and close local
handles when finished. An archive bound to one GlobalPatch cannot read another
revision; serving multiple manifests requires separately scoped handles.

A locator is an untrusted routing hint. Read admits every hinted repository before
storage access, then checks the complete stored roster, organization, GlobalChangeID,
hash and length before returning any manifest. Omitting or substituting a repository
cannot reveal the stored manifest. Storage corruption, wrong scope, denied quota,
revocation and adapter errors return no result; private adapter errors are sanitized.
A failed final admission can leave an immutable artifact but returns no successful
receipt. Retention and storage quotas must be enforced by the deployed quota/store
composition, as for other evidence archives; this milestone adds no deletion jobs.

## Entitlement configuration

The stable identity is `feat_0043` / `graph.global_change`. Migration 000018 publishes
`enterprise-2026-v4`, inheriting all mappings and limits from the sealed
`enterprise-2026-v3` and enabling this capability. The other four editions remain
unchanged. `config.GlobalPatchEditions` loads the matching declarative provisioning
profile using the existing additive bundle loader. CompositionGuard allocations
and all historical profiles remain intact.

Existing subscriptions, licenses, grants and quotas are not rewritten. Provisioners
must apply the migration and explicitly select the new sealed bundle. Existing
SaaS offers/bootstrap remain pinned until configured for rollout. Later commercial
reassignment needs only a new entitlement bundle and its provisioning migration;
there are no edition branches in the domain, service or archive. Tests add Business
access purely through bundle data and exercise the real entitlement resolver.

The shared OpenAPI specification contains GlobalPatch, GlobalChangeID and locator
schemas. No new HTTP endpoint, binary, release stream or edition-specific schema
is introduced.

## Validation and remaining boundaries

Tests cover deterministic and immutable identities; all identity inputs; dependency
cycles; malformed/oversized input; decoder fuzzing; all five edition allocations;
independent permissions; quota denial; multi-repository and tenant isolation;
tampered/subset locators; corrupt adapters; cancellation; revocation during storage;
idempotent local archive round trips; and downgrade/re-enable without data loss.
Migration tests compare SQL with embedded configuration, preserve sealed history,
subscriptions and quota data, and reject migration reapplication. Native PostgreSQL
testing remains opt-in with `SAFEPATCH_POSTGRES_MIGRATION_TEST=1`, `psql` and libpq
connection settings for a disposable database.

This milestone implements identity, immutable storage and guarded application
operations. Live HTTP/worker wiring, automatic dependency discovery, coordinated
patch execution and distributed transaction/rollback orchestration are not included.
Verification continues through the existing shared core. A GlobalChangeID is not
evidence of PASS; UNKNOWN remains UNKNOWN in that core, and no LLM can authorize
production through these manifest operations. Existing v1.0 release blockers remain.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...`, `go vet ./...`,
focused integration/migration tests and OpenAPI YAML parsing passed. The native
`TestPostgresGlobalPatchMigration` was skipped because the PostgreSQL environment
was not configured. No deployment migration was applied and no release was made.
