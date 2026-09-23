# Edition metadata and immutable bundles

SafePatch has exactly five permanent edition keys: `community`, `developer`, `team`, `business`, and `enterprise`. The shared implementation lives in `internal/domain/edition`; all editions use the same packages, schema, migration history, and release stream.

**Software Version != Commercial Edition != Release Channel.** An edition-version identifier such as `team-2026-v1` describes a commercial bundle revision. It is not a software version or update channel. `team-2027-v2` is a new bundle, not a replacement for the old one.

## Domain API

- `Editions()` and `Lookup(Key)` expose fixed edition keys/display names. They have no capability-enabling behavior.
- `EditionVersion` contains `edition_version_id`, `edition_key`, and `created_at`.
- `EditionFeature` contains `edition_version_id`, `feature_id`, `enabled`, nullable `limit_value`, and `configuration_json`.
- `NewCatalog(featureCatalog)` binds bundle validation to the existing FeatureCatalog.
- `Catalog.Publish(version, mappings)` validates a complete bundle and returns a new immutable history snapshot. Reusing an ID always returns `ErrVersionExists`; the existing snapshot remains unchanged on success or failure.
- `GetVersion(id)` and `GetFeature(versionID, featureID)` return defensive copies. `Versions(key)` lists IDs in lexical order; it deliberately does not choose a latest version.

The subsequent [initial bundle milestone](initial-edition-bundles.md) seeds the approved commercial allocations through an additive migration. `enabled` is commercial mapping metadata, not an effective entitlement or permission decision. The presence of `enterprise` in an edition key never enables code. Future access services must evaluate `EntitlementService.Has(...)`, `EntitlementService.Limit(...)`, and `PermissionService.Allowed(...)` separately.

## Validation and ownership

Version IDs follow `<permanent-key>-YYYY-vN`, with a four-digit year and positive revision of at most nine digits. The prefix must match the edition key. Registration timestamps must be nonzero, JSON-compatible values and are normalized to UTC. IDs are selected explicitly; no wall clock, auto-incrementing commercial revision, release version, or channel is inferred.

Each feature ID must resolve through FeatureCatalog. A version cannot contain duplicate mappings or mappings for another version. Enabled features require their catalog prerequisites to be enabled in the same bundle. This validates consistency without executing the feature or asserting that a PLANNED feature is available at runtime.

`limit_value = null` means unspecified, `0` is an explicit zero, and positive values are explicit limits. Negative values are rejected; null never means unlimited. The future entitlement layer must interpret units using approved feature/limit policy. `configuration_json` defaults to `{}` when omitted in the Go input. It must be a JSON object, not null, an array, or executable instructions.

Go validation rejects duplicate JSON object keys at every depth, invalid UTF-8, unpaired UTF-16 surrogate escapes, NUL values, trailing data, and nesting beyond 32 levels. Numeric exponent magnitude is bounded to 1000 while numeric text is preserved without float conversion. Each configuration is limited to 16 KiB and aggregate configuration to 1 MiB per bundle; a bundle has at most 4096 mappings. The in-memory history is bounded to 4096 versions. Callers must bound input bytes before JSON decoding at future transport boundaries.

Inputs, pointer-valued limits, JSON buffers, and results are copied. Concurrent read use is safe; callers must not concurrently mutate constructor/publication inputs. Immutable publication returns a new snapshot, so its owner must coordinate concurrent writers when replacing its current reference. The database, rather than independently branched in-memory snapshots, is the authority for global version-ID uniqueness.

## Shared PostgreSQL schema

The first migration, [000001_editions.up.sql](../../migrations/000001_editions.up.sql), adds:

| Table | Purpose |
| --- | --- |
| `editions` | Exactly five seeded key/name pairs, constrained against additional keys and protected against modification/removal |
| `edition_versions` | Permanent version ID, edition foreign key, creation time, and an internal sealing marker |
| `edition_features` | Version/feature composite primary key, enabled flag, nullable nonnegative bigint limit, and bounded JSONB object |
| `feature_identities` | Minimal foreign-key registry for the 29 existing FeatureCatalog ID/key pairs; no grants or duplicate feature implementation |

The feature identity table is a prerequisite for referential integrity, not full FeatureCatalog persistence. Future catalog additions require additive migrations preserving the existing ID/key bindings. SQL seeds are tested against the Go catalog. SQL does not currently persist feature dependency metadata; use domain validation before publication, including enabled prerequisite checks.

`publish_edition_version(version_id, edition_key, registered_at, mappings)` inserts a complete version and its mappings, then seals it in the same transaction. A deferred constraint trigger prevents committing an unsealed version. The only permitted version-row update is that initial sealing transition. Once sealed, configuration cannot be extended, overwritten, deleted, or truncated. Mapping updates and deletes are forbidden even during assembly; a failed assembly must roll back and restart. Reapplying the migration fails rather than upserting over history.

Feature insertion locks its parent version row to serialize insertion against sealing. Duplicate IDs and feature mappings are rejected by keys; missing features/editions are rejected by foreign keys. Fixed function search paths include the installation schema before `pg_temp`, protecting guard queries from caller/temp-table shadowing. Functions use invoker rights, and publication execution is revoked from PUBLIC. A future restricted publisher role needs explicitly granted function/table privileges; no runtime role or application persistence adapter is installed here.

PostgreSQL JSONB canonicalizes object data and cannot recover duplicate raw JSON keys; the Go validator must run before conversion to JSONB. SQL enforces object type and serialized-size limits; its representation may be larger than Go's compact JSON and may reject a near-boundary input. SQL additionally checks aggregate size at sealing. History protections apply to ordinary data operations; a database owner/superuser can alter schema or disable triggers and remains trusted.

These tables contain global product metadata, not tenant-owned records. Organization assignments are deliberately outside this milestone. A future assignment must reference an exact edition-version ID with tenant-scoped access checks; it must never dynamically follow the newest version for an edition.

## Grandfathering

An organization assigned to `team-2026-v1` retains that bundle when `team-2027-v2` is published. Neither publishing nor reading a new version updates an older mapping, limit, configuration, or creation timestamp. Moving a customer requires a separate explicit assignment change. Keep old versions referenced and readable; do not migrate customers by overwriting historical rows.

Tests publish different old/new mappings for all five editions, pin the old ID, and verify unchanged history. Those are bundle-preservation tests; they do not claim a working customer-assignment or entitlement system.

## Validation and remaining work

Unit/security/error-path tests cover permanent keys, version ownership, unknown and duplicate feature IDs, required dependencies, disabled/zero/unspecified values, hostile configuration, bounds, defensive copying, JSON round-trips, and concurrent readers. SQL assertions cover publication for all five editions, foreign keys, invalid mappings, transactional failure, history mutation/extension/removal attempts, temporary-table shadowing, and version-ID reuse.

See [migration instructions](../../migrations/README.md) for the opt-in native PostgreSQL test. There is no destructive down migration: rollback of commercial history must not silently erase customer bundles. No premium workflow, entitlement enforcement, authorization service, organization assignment, billing adapter, API endpoint, or migration runner is implemented in this milestone.

Validated on 2026-09-06 with Go 1.26.5, Windows/amd64: `go test ./...`, `go test -race ./...`, and `go vet ./...` passed. Formatting and Markdown relative-link checks passed. The native PostgreSQL test was skipped because no native server/client was installed or configured. The migration and full SQL assertion suite were executed successfully in a disposable PostgreSQL 18.3 WASM instance using [PGlite](https://pglite.dev/docs/) 0.5.8, installed only in a temporary test directory. Migration reapplication was rejected and all 11 test versions remained sealed. This validates SQL behavior in that engine, not native multi-connection concurrency, deployment privileges, or production readiness. Fuzz seeds ran during the Go suites; no extended fuzz campaign ran.

Next recommended prompt: implement tenant-scoped organization assignments pinned to edition-version IDs, with separate entitlement and permission checks and explicit upgrade/downgrade behavior.
