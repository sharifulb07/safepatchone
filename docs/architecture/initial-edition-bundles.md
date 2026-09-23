# Initial edition entitlement bundles

The initial commercial allocations are seed data in [000002_initial_edition_bundles.up.sql](../../migrations/000002_initial_edition_bundles.up.sql). The migration adds nine missing capability identities and publishes five immutable bundles using the existing `publish_edition_version` function. There is no new edition-based application branching, entitlement evaluator, or authorization logic.

| Edition | Pinned version | Enabled mappings |
| --- | --- | --- |
| Community | `community-2026-v1` | 4 |
| Developer | `developer-2026-v1` | 9 |
| Team | `team-2026-v1` | 16 |
| Business | `business-2026-v1` | 23 |
| Enterprise | `enterprise-2026-v1` | 32 |

Each version has a fixed registration timestamp of `2026-09-06T00:00:00Z`. The migration's `$bundles$` JSON document is the canonical allocation data. Inheritance is fully expanded into 84 mappings; readers never follow a live parent edition, infer ordering, or compare edition names to enable features. Every mapping has `enabled: true`, `limit_value: null`, and `configuration_json: {}`. Null remains **unspecified**, not unlimited. No quotas or extra configuration were provided, so none are invented.

## Allocation contract

- **Community:** `verify.basic`, `verify.security`, `intent.basic`, `graph.local`.
- **Developer adds:** `intent.advanced`, `proof.planner`, `proof.cloud_history`, `integration.github.private`, `language.additional_packs`.
- **Team adds:** `platform.organizations`, `policy.central`, `graph.organization`, `proof.passport`, `proof.shared`, `audit.organization`, `verify.pr_enforcement`.
- **Business adds:** `graph.cross_repo`, `testtrust`, `runtime.replay`, `runtime.behaviordna`, `business.invariants`, `incident.antibodies`, `verify.composition`.
- **Enterprise adds:** `agent.mcp_gateway`, `agent.safelease`, `agent.temporalguard`, `proof.living`, `enterprise.sso`, `enterprise.audit`, `enterprise.self_hosted`, `enterprise.airgap`, `enterprise.lts`.

## New stable capability identifiers

These keys encode the named requirements without adding edition tokens or reusing a broader capability accidentally:

| Requested item | Stable key | Permanent ID |
| --- | --- | --- |
| Cloud proof history | `proof.cloud_history` | `feat_0030` |
| Private GitHub integration | `integration.github.private` | `feat_0031` |
| Additional language packs | `language.additional_packs` | `feat_0032` |
| Organizations | `platform.organizations` | `feat_0033` |
| Central policy | `policy.central` | `feat_0034` |
| Shared proofs | `proof.shared` | `feat_0035` |
| Team audit | `audit.organization` | `feat_0036` |
| PR enforcement | `verify.pr_enforcement` | `feat_0037` |
| Composition verification | `verify.composition` | `feat_0038` |

The original 29 ID/key pairs remain unchanged. The FeatureCatalog now has 38 metadata definitions, all still `PLANNED`. The nine additions are definitions, not implementations. `audit.organization` is separate from `enterprise.audit`, and additional language packs do not automatically grant `verify.polyglot`.

`verify.api`, `verify.polyglot`, `intent.specforge`, `runtime.shadow`, `agent.provenance`, and `agent.mcp_inventory` remain unmapped in these initial bundles. An omitted mapping must not become an implicit grant.

## Upgrade and grandfathering

Apply migration 000001 followed by 000002 using the single shared migration history. Migration 000001 is unchanged. Migration 000002 uses inserts and immutable publication only; it never updates, deletes, or upserts historical data. A collision with an existing ID aborts the entire transaction, including any new identities or bundles inserted earlier in that transaction. Resolve such a collision with a reviewed additive migration and new version IDs, never by overwriting the existing version or modifying applied seed data.

Future bundle changes must publish a new ID, such as `team-2027-v2`. Customers pinned to old IDs retain their original mappings and limits. Publishing these bundles does not assign or move any customer.

**Software Version != Commercial Edition != Release Channel.** The 2026 commercial bundle revision does not select a software release or channel. The `enterprise.lts` mapping does not itself select LTS deployments.

## Tests and scope

Go tests decode the actual seed document, pass it through the existing domain validators, and compare every registered feature against each edition's expected boundary. They check cumulative counts, exclusions, deterministic metadata, unspecified limits, immutable snapshots, malformed mapping failures, and preservation after a new bundle is published. Feature identity tests validate the full additive migration history against FeatureCatalog.

SQL fixtures cover an existing pinned `team-2025-v1` bundle, exact database allocations, sealed history, mutation rejection, new-version preservation, reapplication rejection, and complete rollback when `team-2026-v1` already exists. Native integration tests are opt-in through the [migration instructions](../../migrations/README.md).

These are commercial configuration boundary tests. EntitlementService, PermissionService, tenant assignments, and premium runtime workflows are not implemented, so these tests do not claim actor authorization or production feature access. Future access services must require both entitlement and authorization; neither edition metadata nor a PLANNED catalog entry can directly authorize production.

Validation on 2026-09-06: `go test ./...`, `go test -race ./...`, and `go vet ./...` passed on Go 1.26.5, Windows/amd64 after retrying outside the sandbox due to a build-cache access denial. Both SQL fixture paths (successful upgrade and conflicting-version rollback), plus reapplication rejection, passed in the existing temporary PGlite PostgreSQL WASM engine. Native PostgreSQL integration remains opt-in and was skipped locally; native multi-connection behavior is not claimed. Formatting and relative documentation links passed, and migration 000001's SHA-256 remained unchanged.

Next recommended prompt: implement tenant-scoped entitlement resolution from a pinned edition-version assignment, with separate actor permission checks and explicit handling of unspecified limits and planned capabilities.
