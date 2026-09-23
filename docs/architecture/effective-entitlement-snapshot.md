# EffectiveEntitlementSnapshot

SafePatch now has a derived organization entitlement snapshot and a short-lived in-process cache. One refresh loads the organization's canonical records once and reuses the existing EntitlementResolver locally for every catalog capability. Repeated operations check authoritative source revisions without repeatedly loading and resolving hundreds of database rows.

This milestone provides the domain model, SnapshotSource adapter contract, SnapshotService, invalidation entry point, and tests. There is no production database adapter, change-event consumer, or verification execution path yet. The subsequent [PermissionService milestone](permission-service.md) adds independent actor permission checks and shared request orchestration. No schema, migration, customer assignment, or edition seed changes are introduced here.

## Data and API

The domain type is `internal/domain/entitlement.EffectiveEntitlementSnapshot`:

| Field | Meaning |
| --- | --- |
| `organization_id` | Required tenant scope |
| `edition` | One of the five permanent metadata keys |
| `edition_version` | Pinned immutable commercial bundle identity, such as `team-2026-v1` |
| `features` | Every registered stable feature key mapped to its resolved enabled state |
| `limits` | Feature-keyed nullable numeric limits, matching the current canonical record model |
| `generated_at` | UTC evaluation instant captured before the input load |
| `valid_until` | Exclusive UTC deadline; no later than the TTL or earliest scheduled record transition |
| `source_versions` | Nonzero revisions for subscription, trial, license, manual override, edition configuration, community default, and promotion |
| `content_hash` | SHA-256 of canonical JSON, with sorted map keys, UTC timestamps, and an empty `content_hash` field |

Limits are not new independent quota dimensions. The current entitlement model provides one optional limit per feature. Null remains unspecified, never unlimited; a disabled feature has an explicit zero. Named resource quotas and atomic usage reservation require their own milestone.

```go
service, err := entitlement.NewSnapshotService(
    source, featureCatalog, time.Now, 30*time.Second, 128,
)
// service.Snapshot(ctx, orgID) (domain.EffectiveEntitlementSnapshot, error)
// service.Has(ctx, orgID, featureKey) (bool, error)
// service.Limit(ctx, orgID, featureKey) (int64, error)
// service.Resolve(ctx, orgID, featureKey) (Resolution, error)
// service.Invalidate(ctx, orgID, entitlement.SubscriptionChanged) error
```

SnapshotService implements the existing EntitlementService interface. Has is false on errors. Limit preserves the resolver's denial and unspecified-limit errors. Resolve preserves the source, winning-record expiration, and deterministic explanation evaluated at snapshot generation. That winning-record expiration is still not a cache TTL; the service enforces the separate snapshot deadline.

Snapshot maps and pointers are copied on return. Snapshots are metadata values, not bearer tokens. The content hash detects corruption and supports comparisons; anyone able to change data can recompute it. A valid hash or `ValidateAt` result alone does not prove source authority, current ownership, or actor permission. No caller-supplied snapshot can be inserted into the service cache.

## Consistency and invalidation

The new SnapshotSource contract is separate from the paginated EntitlementRepository, whose independent List calls cannot promise a consistent snapshot:

1. `CurrentState(ctx, org)` reads authoritative assignment metadata and all source revisions. It must not read a stale replica or a TTL cache.
2. `Load(ctx, org)` returns all canonical records and the matching state from one storage snapshot. Include disabled, expired, and future records. A successful empty result must mean no grants, never a hidden storage error or truncated result.
3. On a miss, the service compares the load's state with the initial state, resolves locally, then reads CurrentState again. Any mismatch or local invalidation during evaluation returns an error without an allow or partial cache entry.
4. On a hit, it compares current state, tenant, hash, and validity window before returning the private cached decision. No record load is needed.

Every source mutation must atomically advance the corresponding revision with its canonical data change. This includes subscription status/quantity changes, trials, license replacement or revocation, manual restrictions, deletion, defaults, promotions, and edition assignment/configuration changes. Revisions begin above zero even when a source has no grants and must never be reused, reset, or wrapped. Restore workflows must preserve this property. Any input capable of changing a resolution must be represented by these revisions or a scheduled transition in the loaded records.

After commit, adapters may call `Invalidate` with SubscriptionChanged, TrialChanged, LicenseChanged, ManualOverrideChanged, EditionConfigurationChanged, CommunityDefaultsChanged, or PromotionChanged. It evicts the target organization's entry and fences in-flight evaluations. The fence is process-wide, so a concurrent evaluation for another tenant may conservatively return ErrSnapshotChanged; unrelated completed entries remain cached. Event delivery is an optimization: source-revision checks handle missed, duplicate, delayed, or reordered notifications. Invalidate alone does not perform a durable revision update or revoke a grant.

Edition configuration updates mean publishing new immutable configuration and changing an explicit assignment or its revision as appropriate. Existing historical bundle versions and grandfathered customer assignments must not be overwritten. Publishing a new bundle does not silently switch every customer to it. The adapter must verify the pinned edition version exists; the snapshot validates its syntax and matching permanent edition key. Edition metadata itself never enables a feature.

## Expiry and execution boundary

Configured TTL must be positive and at most one minute. `valid_until` is the minimum of generation plus TTL and every future start/expiration in the complete input. This conservatively includes non-winning grants and ignored sources. A paid license with no expiration therefore cannot hide a security restriction scheduled to start shortly afterward.

Validity is `[generated_at, valid_until)`. At the exact deadline the cache must refresh successfully or deny. Errors, cancellation, changed sources, resource-limit violations, or a refresh that finishes after its own deadline return no usable snapshot. There is no stale-on-error or background stale-while-refresh mode, including for low-value use. A known clock rollback behind a cached generation also fails closed; hosts still need a trusted, synchronized clock.

High-value paid execution must call the service at admission and honor errors and denial. Never enqueue a copied snapshot and later trust its `features` map without fresh admission. Long-running or queued work needs a separate lease/recheck policy. A revision can change immediately after the last authoritative read: this cache does not provide a transaction spanning entitlement evaluation and starting production work. An atomic admission/revocation workflow remains required where that race is unacceptable.

Actor authorization remains a separate mandatory check through PermissionService.Allowed, followed by policy, quota, and implementation-readiness checks. Neither cached ownership nor an LLM authorizes production. PLANNED capabilities remain unimplemented. Software Version != Commercial Edition != Release Channel.

## Bounds and verification

The cache holds a configured 1–1024 organization entries, evicting the earliest deadline when full. A refresh accepts at most 4096 records and 2 MiB of aggregate metadata for the whole organization. Adapters must enforce equivalent read bounds before allocating untrusted results. Cold concurrent misses may each perform a bounded refresh; concurrency/admission limits belong at the caller or adapter. Cache state is disposable and local; distributed invalidation transport and persisted snapshots are not implemented.

Tests cover row-load reuse, deterministic hashing, defensive copies, corruption, tenant isolation, resolver parity, null limits, every source revision and invalidation event, scheduled restriction starts/expiry, TTL boundaries, slow refresh, read failures, cancellation, changing inputs, concurrency, cache capacity, and malformed/resource-heavy inputs. Integration fixtures run the migration's five edition bundles through canonical grants, the snapshot service, and a separate test permission check, then simulate subscription changes while preserving historical values. These fixtures do not replace a real database/permission integration test. Existing native PostgreSQL migration tests remain opt-in.

Next recommended prompt: implement a PostgreSQL SnapshotSource with atomic source revisions, consistent record loading, and mutation/invalidation integration tests; preserve immutable edition history and separate actor authorization.
