# One codebase, five editions

Status: architecture requirements. The [current-state audit](current-state.md) found an empty workspace. The subsequent [repository structure milestone](repository-structure.md) establishes shared package boundaries and import enforcement; the feature and infrastructure services described here remain unimplemented.

## Permanent product constraints

SafePatch has exactly five permanent commercial editions: **Community, Developer, Team, Business, Enterprise**. These are commercial configurations of one platform.

Maintain one repository, one Go domain architecture, one API specification, one database schema, one verification engine, one release stream, and one migration history. Multiple process entry points may serve different operational roles; none is an edition-specific application. Do not introduce per-edition forks, duplicated packages, database tables that reproduce the same domain by edition, API specifications, migration trees, or edition-specific builds.

**Software Version != Commercial Edition != Release Channel.** Version identifies software behavior and compatibility. Edition identifies a commercial offering mapped to grants and limits. Channel identifies the update/support track. Neither edition nor channel is a semantic-version component. See the [matrix](../roadmap/version-edition-matrix.md).

## Capability and limit vocabulary

The [FeatureCatalog](feature-catalog.md) implements the full 29-key vocabulary, metadata validation, and immutable lookups. It does not grant access. The list below retains the architecture's initial examples; see the catalog document for the complete ID/key contract.

Stable feature keys are contract identifiers, not display names:

```text
verify.basic
intent.basic
intent.advanced
graph.local
graph.organization
graph.cross_repo
proof.planner
proof.passport
runtime.replay
runtime.shadow
business.invariants
incident.antibodies
agent.mcp_gateway
agent.safelease
proof.living
enterprise.sso
enterprise.self_hosted
enterprise.airgap
```

The explicitly required `enterprise.*` keys and `business.invariants` are capability-namespace exceptions to the rule against edition names in keys; they must not cause runtime edition comparisons. The FeatureCatalog rejects other edition tokens in new keys. Additional feature keys require deliberate contract design, not spelling variants or UI-created identifiers.

Limit keys:

```text
repositories
developers
proof_credits
replay_requests
retention_days
verification_concurrency
artifact_storage
fuzz_seconds
```

Define units, scope, accounting period, and reservation/refund semantics for each limit before enforcing it. In particular, storage should have an explicit byte unit; credits and requests need explicit accounting windows. Distinguish a missing limit, a zero limit, and explicitly unlimited access. A missing key or lookup failure must never mean unlimited. Longer retention is not interchangeable with a consumable quota.

No edition-to-feature allocation or commercial quota value is approved by this document. The catalog must contain exactly the five editions; approved catalog configuration maps them to grants and limits. Do not infer that every higher edition inherits every lower edition's feature or that Community lacks basic capabilities.

## Entitlement, permission, and policy

Use contracts equivalent to `EntitlementService.Has(...)`, `EntitlementService.Limit(...)`, and `PermissionService.Allowed(...)`. Arguments must include trusted organization scope; permission decisions also require the authenticated actor, action, and resource. The signatures and storage implementation remain a foundation-milestone decision.

An organization owning a capability does not authorize every member to use it. An actor having a permission does not grant an unpurchased capability. Authentication, tenant membership, entitlement, permission, quota, and applicable policy must all pass before a side effect occurs.

For a protected operation:

1. Authenticate the user/service account and resolve validated organization membership. Treat an organization ID in a request as a selection to validate, never as proof of membership.
2. Load the resource through a tenant-scoped repository. Reject references to another tenant without disclosing its contents.
3. Check the required capability and the actor's action/resource permission independently. Either denial blocks access. Return decision errors explicitly; UNKNOWN and service failures do not become allow.
4. Apply policy and reserve any quota atomically with admission to work. Use idempotency to prevent retries from double-charging or bypassing limits.
5. Record admission context, entitlement revision, actor, policy version, and usage reservation. Workers validate tenant/resource bindings and recheck authority before sensitive effects.

Illustrative decision rule: `allow = authenticated AND tenant_member AND entitled AND permitted AND within_limits AND policy_allows`. An LLM output is never an authorization input that can independently allow production.

UI controls can use effective capabilities to explain availability. Every protected API, CLI operation, worker action, gateway call, and artifact access must enforce its own trusted boundary. Hiding a button is not enforcement. Avoid `if plan == "enterprise"` in business logic; commercial names belong in catalog mapping, license validation, and presentation.

## Modular Go architecture

The following responsibilities map to the shared package scaffold; their service implementations remain future work:

| Boundary | Responsibility |
| --- | --- |
| Domain | Tenant/resource identity, verification outcomes, intent, graph, proof, provenance, replay, and policy rules |
| Application services | Coordinate domain operations, capability/permission checks, transactions, quotas, and work admission |
| Entitlement and licensing | Resolve effective grants, limits, validity, and grant revisions from approved catalog/license inputs |
| Authorization | Actor membership, roles, resource permissions, and revocation |
| Infrastructure adapters | PostgreSQL, NATS JetStream, S3-compatible storage, scanners, sandbox runners, billing providers, and OpenTelemetry |
| Delivery/composition | Shared API handlers, CLI, workers, and agent/MCP gateway with injected dependencies |

Domain code depends on typed contracts, not Next.js, payment-provider SDKs, transport handlers, or infrastructure clients. The composition root wires implementations. Share implementations across editions and process roles; avoid global mutable tenant or license state.

Go owns platform, API, CLI, workers, verification, policy, graph, proof, provenance, replay orchestration, and the gateway. Next.js/TypeScript serves marketing and dashboard only. PostgreSQL owns transactional metadata; NATS JetStream carries durable asynchronous work; S3-compatible storage holds large immutable evidence/artifacts; OpenTelemetry supplies observability.

Billing adapters validate provider events and translate them into versioned entitlement changes. Verification and other domain modules consume effective grants, never payment status, SKU strings, or provider-specific subscription objects. Offline licensing supplies the same grant contract without requiring a billing network call.

## Tenant and persistence boundaries

- Every tenant-owned metadata row and repository operation carries an organization ID. Composite constraints must prevent relationships across organizations. If PostgreSQL row-level security is used, validate transaction-scoped tenant context and connection-pool behavior; it supplements application enforcement.
- Queue envelopes bind organization, actor/service identity, resource IDs, operation ID, and schema version. Consumers validate those bindings, reauthorize sensitive actions, and handle at-least-once delivery idempotently. Use a transactional outbox or equivalent to avoid committed metadata with lost work.
- Artifact metadata binds tenant, digest, size, media type, and provenance. Server-generated object keys and scoped access prevent path guessing and cross-tenant downloads. Immutable storage does not by itself prove evidence authenticity.
- Cache keys include tenant and relevant entitlement/permission revisions. Invalidation and bounded validity must cover grants, downgrades, membership changes, and revocation.
- Logs, traces, exports, search indexes, and organization/cross-repository graphs preserve the same tenant boundaries. Cross-repository capability grants do not authorize cross-organization access.

All editions use the same tables and ordered migrations, even when capabilities are disabled. Applying a migration must not depend on a tenant's edition. No schema is introduced by this audit.

## Edition changes and licensing

Change effective grants through validated, auditable configuration or licensing, rather than rewriting domain objects. Keep software version/channel outside grant assignment.

Upgrades enable approved capabilities without copying data. Downgrades block new restricted operations and preserve existing evidence and references. Define separately whether historical data remains readable/exportable under the new grant set; ordinary tenant and actor authorization always applies. Reducing a quota must not silently delete artifacts or corrupt records. Existing usage above a new cap can block new allocation while preserving stored data. Retention deletion requires an explicit lifecycle policy, including any holds.

Document treatment of queued/running work for grant expiry, revocation, and downgrade before shipping asynchronous premium features. Recheck before sensitive effects and record the effective decision; do not let an indefinitely valid admission snapshot bypass revocation.

Validate license signatures, issuer, organization binding, validity window, supported format, and anti-rollback/replay policy. Do not trust client-editable edition strings. Self-hosted and air-gapped deployments use shared domain interfaces with configured adapters. Any offline grace and revocation limitations must be explicit; lookup failure cannot implicitly grant features.

## Required acceptance tests

For every premium capability, test entitled success, non-entitled denial, permission denial after entitlement passes, and upgrade/downgrade data preservation. Execute this against the shared service and relevant API/worker paths, not only a UI mock.

Also require unknown-key and lookup-error denials, zero/unlimited/missing-limit behavior, concurrent quota reservation, idempotent retries, revoked memberships, forged tenant references, cross-tenant artifacts/jobs/cache access, stale grants, and invalid/expired licenses. Migration tests exercise the same migration chain across all five edition fixtures and assert domain data survives entitlement changes. Run `go test ./...` and `go test -race ./...` after meaningful implementation changes.
