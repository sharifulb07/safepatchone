# Canonical Entitlement model

`internal/domain/entitlement` defines the canonical organization grant record. `internal/entitlement` defines `EntitlementRepository`. Both are independent of billing providers, database drivers, editions, and actor authorization. There are no Stripe SDK objects or subscription-provider schemas in these contracts.

Subsequent milestone: [EntitlementResolver](entitlement-resolver.md) now consumes these records and implements the specified precedence. The model/persistence boundaries documented below remain distinct from that resolver and actor authorization.

## Record contract

| JSON field | Meaning |
| --- | --- |
| `entitlement_id` | Stable opaque record ID within organization scope |
| `organization_id` | Organization that owns the record; an ID alone does not prove membership |
| `feature_id` | Stable FeatureCatalog ID, not an edition or display name |
| `source` | One of the six canonical source identifiers below |
| `source_reference` | Required opaque reference to the originating record, license, promotion, override, or pinned bundle |
| `enabled` | Explicit record state; false remains false regardless of metadata |
| `limit_value` | Nullable nonnegative int64; null means unspecified, zero is an explicit zero, neither implicitly means unlimited |
| `starts_at` | Required inclusive start instant |
| `expires_at` | Optional exclusive end instant; null means no scheduled expiration |
| `metadata` | Inert, bounded JSON object; omitted Go input is normalized to `{}` |
| `created_at` | Required record-creation timestamp, independent of the validity window |

Sources are exactly `COMMUNITY_DEFAULT`, `SUBSCRIPTION`, `TRIAL`, `ENTERPRISE_LICENSE`, `PROMOTION`, and `MANUAL_OVERRIDE`. Unknown, empty, provider-specific, or differently cased source values fail validation. Source identifies provenance; it does not prove payment, validate a license signature, assign source precedence, or authorize an override. A future trusted adapter must verify its input before creating records.

IDs are 1–128 ASCII letters/digits/underscore/hyphen, starting with a letter or digit. References are 1–256 ASCII characters and additionally allow dot, colon, and slash for namespacing. References are not opened as URLs or paths; they must contain record references rather than secrets or serialized provider objects. For bundle-derived grants, preserve the exact edition-version reference, such as `team-2026-v1`, rather than a mutable `team` label.

## Construction and lifecycle

`NewEntitlement(input, featureCatalog)` validates the record and existing feature identity, normalizes timestamps to UTC, compacts metadata, and copies pointer/slice values. It returns a zero record on failure. Feature catalog membership is checked without claiming that a PLANNED capability is implemented. Organization existence, source authenticity, and actor permissions cannot be verified by this constructor.

`Entitlement.Validate()` checks structural validity only. Values decoded from JSON or read from storage must be validated; unmarshalling is not a grant or a validation boundary. Constructors and adapters must additionally check referenced resources. All times are caller supplied; there is no hidden system-clock lookup.

`ActiveAt(at)` returns `(bool, error)` for the record's enabled and temporal state. Start is inclusive and expiry exclusive. Invalid records or evaluation times return false with an error. An absent expiry is allowed. A present expiry must follow the start. Creation may occur after the start or expiration when importing historical records.

`ActiveAt` is deliberately **not** `EntitlementService.Has`: it does not resolve conflicting records, account for quotas, check implementation readiness, authenticate an actor, or authorize production. In particular, a time-active record with a zero limit does not imply available quota. `metadata` cannot override organization, source, enabled state, time window, or limits, and LLM instructions stored in metadata have no authority.

`Clone()` copies metadata bytes, limit pointers, and expiry pointers. These are value snapshots with exported fields; callers must own their copies and must not concurrently mutate values that another goroutine is reading. A new edition bundle does not mutate previously constructed grant records or their source references.

## Shared metadata validation

The existing edition configuration validator was moved to `internal/domain/jsonobject` and reused. The edition wrapper preserves its original error contract. Entitlement metadata has a 16 KiB limit, a 32-level nesting limit, and the existing numeric exponent bound of 1000. It rejects non-objects, duplicate keys at every depth, invalid UTF-8, unpaired surrogate escapes, NUL, malformed or trailing JSON, and oversized inputs. Numeric text is retained without conversion to floating point. Future transport adapters must also bound incoming payload bytes before decoding.

## EntitlementRepository

The interface exposes:

```go
Create(ctx context.Context, organizationID string, record domain.Entitlement) error
Get(ctx context.Context, organizationID, entitlementID string) (domain.Entitlement, error)
List(ctx context.Context, organizationID string, query ListQuery) (Page, error)
```

Each method requires explicit trusted organization scope. `ValidateCreate`, `ValidateGet`, and `ValidateList` provide reusable request validation. They do not prove organization membership, query storage, or automatically enforce an implementation's behavior.

Adapter requirements:

- Create must reject organization mismatch, validate feature/organization references and canonicalize the record, and atomically reject a duplicate `(organization_id, entitlement_id)` with `ErrAlreadyExists`. It must not upsert or replace prior data. Separate records for the same organization and feature are allowed because sources may coexist.
- Get must query by both organization and entitlement ID. Missing and other-organization records both return `ErrNotFound`; no unscoped lookup followed by filtering is permitted.
- List must filter in storage by organization, optionally by exact feature ID, and use explicit pages of 1–200 records. `AfterID` is a bounded last-seen entitlement ID with deterministic binary ordering, not SQL or a tenant-selection token. `NextAfterID` is empty when exhausted; otherwise it is the last returned ID. Preserve scope and filters between pages. Concurrent writes can affect pagination; it is not a transactional snapshot.
- List returns canonical records, including disabled, future, and expired records. It does not merge sources, select a winning grant, or return effective access decisions. Do not treat one page as the full set of grants.
- Honor context cancellation/deadlines; return storage failures as errors rather than empty successful results. Copy mutable values at storage boundaries. Validate current organization membership and actor permissions in separate application services.

No unscoped enumeration, update, delete, renewal, or revocation operation is introduced. Those need an explicit lifecycle and audit policy in a later milestone. No repository adapter is implemented here; these are contracts for future persistence, not proven database isolation guarantees.

## Scope and verification

This milestone adds the model, source vocabulary, repository interface, validation helpers, and tests. It does not create organizations, issue live grants, assign subscriptions, change edition seeds, add SQL tables/migrations, resolve entitlement precedence, enforce quotas, validate licenses, or implement `PermissionService`.

Tests cover all eleven serialized fields and six source values, valid and invalid lifecycle boundaries, unknown sources/features, zero/unspecified limits, hostile metadata, UTC normalization, deep copies, concurrent reads of independent copies, scoped repository-query validation, and integration with the existing seeded feature IDs and pinned bundle references. Existing edition and migration-contract tests provide regression coverage for the shared validator extraction. No new database migration tests apply because no persistence was added. Premium runtime access and actor authorization remain unimplemented, so these model tests do not claim to prove those future boundaries.

Next recommended prompt: implement the PostgreSQL EntitlementRepository adapter after defining organization persistence, with tenant-scoped foreign keys, duplicate-write handling, bounded pagination, and storage/error-path integration tests; keep entitlement resolution and actor permissions separate.
