# EntitlementResolver

`internal/entitlement.EntitlementResolver` resolves canonical organization grant records. It uses the existing FeatureCatalog and EntitlementRepository, with an injected clock. It has no edition comparisons, billing-provider SDKs, license parser, tenant assignment logic, or actor identity model.

The subsequent [EffectiveEntitlementSnapshot](effective-entitlement-snapshot.md) milestone adds a separate SnapshotService implementing the same ownership interface. It reuses this resolver over consistent inputs and bounds cached decisions by source revisions and scheduled transitions. The direct resolver described here remains uncached.

## API

```go
resolver, err := entitlement.NewEntitlementResolver(repository, featureCatalog, time.Now)
// Resolve(ctx, organizationID, featureKey) (Resolution, error)
// Has(ctx, organizationID, featureKey) (bool, error)
// Limit(ctx, organizationID, featureKey) (int64, error)
// ResolveAt(ctx, organizationID, featureKey, currentTime) (Resolution, error)
```

`Resolve` captures the injected clock once. `ResolveAt` accepts the evaluation time explicitly and does not call the clock. Identical records, organization, feature, and time produce identical output and explanation regardless of ordering within a repository page. All times in the output are UTC.

The result has `enabled`, nullable `effective_limit`, `source`, nullable `expiration`, and `explanation`. Unknown features, invalid requests, malformed data, cancellation, and repository failures return an error and a disabled result. Known features with no eligible grant return a disabled result without an error. There is no implicit grant or edition fallback: community defaults must exist as organization-scoped canonical records.

## Precedence

Only records whose start has arrived and whose expiration has not arrived are considered. Start is inclusive; expiry exclusive. Structural validation applies even to expired or future records so malformed repository data cannot be silently ignored.

| Priority | Canonical source/state | Effect |
| --- | --- | --- |
| 1 | `MANUAL_OVERRIDE`, `enabled=false`, within its time window | Security restriction; denies even a paid enterprise license |
| 2 | `ENTERPRISE_LICENSE`, enabled, within its window | Enterprise contract/license grant |
| 3 | `SUBSCRIPTION`, enabled, within its window | Active subscription grant |
| 4 | `TRIAL`, enabled, within its window | Trial grant |
| 5 | `COMMUNITY_DEFAULT`, enabled, within its window | Community default grant |

A disabled manual override is explicitly the security-restriction representation for this milestone; no metadata marker can turn a grant into a restriction or lift one. Positive manual overrides and PROMOTION records have no requested granting precedence and are ignored, with their in-window count recorded in the explanation. They cannot grant or upgrade access. Add a deliberate policy change and boundary tests before supporting them as grants.

Disabled non-manual records are inactive grants, not global restrictions. Expired/future restrictions do not block current grants. An active positive manual record does not supersede an active restriction. The trusted writer/repository must supply current canonical state; provenance labels alone do not prove a verified license, active payment, or authorized security override.

Within the same tier, later `starts_at` wins, then later `created_at`, then lexically smaller entitlement ID. This tie-break is fixed and deterministic. The winning record supplies its limit and expiration; values are never added together or maximized. `source_reference` and metadata are not used for precedence.

## Limits, expiry, and explanations

An enabled record with a null limit remains enabled for ownership purposes, but its numeric limit is unspecified. `Limit` returns `0, ErrLimitUnspecified`, never unlimited. An explicit zero returns `0, nil`; positive limits are returned unchanged. A disabled resolution has an effective zero limit, and `Limit` returns `0, ErrNotEntitled`. Other resolution errors propagate with a zero numeric result. `Has` returns false on every error.

Expiration is the selected record's expiry, including a selected restriction's expiry. It is **not a cache TTL**. A future restriction, new grant, revocation, or repository change can alter the result sooner. No cache is retained. Separate calls to Has/Limit/Resolve may see different times or repository state; callers needing one coherent decision should use one Resolve result and an appropriate transactional admission boundary.

Explanations use stable codes and field ordering. Examples:

```text
grant_selected; source=SUBSCRIPTION; entitlement_id=ent_1; evaluated_at=2026-09-06T12:00:00Z; expires_at=none; ignored_unconfigured=0
security_restriction; source=MANUAL_OVERRIDE; entitlement_id=restriction_1; evaluated_at=2026-09-06T12:00:00Z; expires_at=none; ignored_unconfigured=0
no_active_entitlement; evaluated_at=2026-09-06T12:00:00Z; ignored_unconfigured=0
resolution_failed: repository_error
```

Metadata, source references, and backend error strings are excluded from the explanation. Returned errors preserve underlying causes for internal handling; transport handlers must not expose raw backend errors indiscriminately.

## Repository and resource boundaries

The resolver lists only the requested organization and resolved feature ID, follows every page, and decides only after successful completion. A restriction on a later page always overrides a grant on an earlier page. An error on a later page never returns a partial allow.

It validates tenant/feature bindings, canonical record structure, duplicate IDs, page size, and cursor progress. It sorts a private page slice and rejects overlapping/replayed records, empty continuation pages, or cursors that do not match the page's greatest ID. It never modifies repository records. Resolution is capped at 4096 records and 2 MiB of aggregate metadata per organization/feature. Exceeding either cap returns an error and disables the result.

The existing repository interface does not promise a transactional snapshot across pages. A future persistence adapter/admission workflow must address concurrent grant changes and revocations; the resolver cannot prove a consistent database snapshot from independent List calls. Repository completeness, current source validity, and current tenant membership remain trusted adapter/application responsibilities. No PostgreSQL implementation or persistence guarantees are added here.

## Authorization and implementation readiness

This service answers whether the organization owns the capability under the configured precedence. It does not authorize a user/service account. Consumers must require both this decision and `PermissionService.Allowed(...)`, then apply quotas, policy, and implementation-readiness gates. PLANNED catalog status does not create a runtime implementation; catalog status does not erase commercial ownership metadata either.

No LLM output or metadata field can authorize production. Capability dependencies, usage reservation, license validation, billing validation, customer assignment, and permission evaluation remain outside this resolver milestone. The subsequent [PermissionService milestone](permission-service.md) implements independent membership authorization and a guarded application flow; production authentication/storage/quota adapters and protected API endpoints are still absent.

## Tests and scope

Tests cover each precedence level, paid-plan restriction overrides, expired/future records, clock boundaries, tie-breaking and exact explanations, positive manual/promotion exclusion, later-page restrictions and failures, malformed/cross-tenant data, pagination errors, resource limits, null/zero limits, cancellation, defensive copies, and concurrent reads. Seed integration tests exercise all five bundles against every registered capability, show the required caller composition with a test permission service, and check that rereads and simulated plan changes do not mutate historical records. That caller example is not a production authorization engine.

No schema, migrations, seed allocations, or repository mutation operations are changed. Existing native PostgreSQL integration tests remain opt-in; this milestone's resolver tests use controlled repository fixtures.

Next recommended prompt: implement a PostgreSQL EntitlementRepository and a consistent-read/admission strategy for grant changes and security revocation, keeping actor authorization separate.
