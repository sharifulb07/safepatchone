# CompositionGuard

CompositionGuard checks whether pinned component proofs and their interaction
evidence support an explicitly declared composition. It requires evidence for the
whole combined set: individually passing components and even pairwise passing
contracts do not establish that all components work safely together.

One implementation lives in `internal/proof/composition`. It consumes existing
Passport, evidence status and trust-decision types; it does not fork or replace the
verification engine. The hosted `application.CompositionGuard` uses the shared
execution guard with `proof.composition` and independent `proof.execute` permission.
No engine/service code inspects commercial edition names.

## Commercial allocation and migration

The requested feature is a new stable identity, `feat_0042` / `proof.composition`.
The existing `feat_0038` / `verify.composition` remains distinct. It is not renamed
and does not automatically grant access to this operation.

Migration `000017_composition_guard.up.sql` and the matching embedded
`internal/config/composition_bundles.json` publish additive bundles:

| Edition | Composition profile bundle | proof.composition |
| --- | --- | --- |
| Community | community-2026-v3 | Not enabled |
| Developer | developer-2026-v2 | Not enabled |
| Team | team-2026-v2 | Not enabled |
| Business | business-2026-v3 | Enabled |
| Enterprise | enterprise-2026-v3 | Enabled |

`config.CompositionEditions(featureCatalog)` loads the historical v0.2 baseline
plus these declarative updates. The new Business and Enterprise bundles copy all
prior feature mappings and quota templates, add `proof.composition`, then seal.
No existing subscription, entitlement, quota ledger or edition bundle is rewritten.
The source introduction version and release channel remain independent metadata.

Provisioners must apply migration 000017 and explicitly pin the new version before
the organization receives this capability through its authoritative entitlement
source. Existing SaaS checkout offers/bootstrap still use their pinned v0.2
configuration; this milestone does not silently change paid contracts or marketing
claims. No new grant is synthesized from an edition label. Hosted commercial
rollout must select the new profile through its normal provisioning workflow.

To allocate the feature to Team later, publish a new Team bundle based on its
current immutable version, adding enabled `feat_0042`, and persist that bundle
through the same migration/provisioning mechanism. The configuration loader and
CompositionGuard need no code changes. A test adds that Team update solely as
configuration data and exercises the same service with the real entitlement
resolver and PermissionService. A signed-license issuer can likewise include the
capability in the existing license schema; nodes retain only verification keys.

## Domain contract

`composition.New(Fields)` seals a canonical manifest containing organization ID,
composition ID, composition policy digest, component IDs/repository IDs/commits/
Passport digests, and connections with contract digests. It requires 2–128
components, 1–1024 connections and at most 1 MiB. IDs, digests, duplicate components,
duplicate connections, self-edges and unknown endpoints are validated. Component
and connection arrays are sorted before sealing. Cyclic service relationships
are permitted; the guard does not recursively execute a graph or repository code.

`composition.CompositionGuard.Evaluate` consumes:

- A retained authoritative Passport and validity deadline for each component.
- An authoritative attestation for each connection, bound to its ID, the exact
whole manifest digest and composition policy digest.
- A separate combined attestation bound to the composition ID and same complete
  manifest/policy digests, proving the integrated set was actually checked.

Passports must match the pinned organization, repository, commit and digest.
Attestations pin evidence digests and explicit issued/expiry times. Connection IDs
cannot collide with the combined-proof subject, preventing pairwise proof reuse.
Invalid scope, bindings, duplicate/extra evidence, invalid status/digests, future timestamps or
invalid expiry intervals fail with no result. Missing or expired evidence yields
UNKNOWN. Unknown proof coverage, unknown component verification or unknown policy
results never become PASS. Any FAIL with otherwise complete known evidence yields
FAIL; any UNKNOWN dominates, using the existing `trust.Decide` contract.

PASS requires all declared components, connections and the combined proof to pass.
Changing a component, contract, policy or set membership changes the manifest
digest and prevents reuse of old combined attestations. Result validity is capped
at one minute and the earliest still-active evidence expiry. Result bytes include
the organization and manifest digests, checks, evidence references, diagnostic
decision and evaluation/expiry times. Returned fields are copies; reevaluation
does not mutate prior manifests or results. PASS is not production authorization.

This is an explicit manifest, not automatic dependency discovery or a completeness
proof. A malicious producer that omits a dependency cannot be detected from the
manifest alone. A valid JSON document, hash or uploaded PASS is not evidence of
authenticity. Only trusted, complete outputs of the existing verifier/policy
pipeline may satisfy the source port.

## Application boundary

`NewCompositionGuard(tenants, entitlements, permissions, quotas, source, clock)`
constructs the service. Call `Evaluate(ctx, authenticatedActor, organizationID,
compositionID)`. The initial organization-scope guard checks membership, capability,
permission and quota before loading the manifest. Each distinct repository then
passes current tenant/capability/permission checks before evidence is loaded.
Quota is admitted once for the composition, not once per access recheck.

The `CompositionSource` port separates manifest lookup from evidence lookup so a
foreign repository in a manifest cannot cause an unauthorized proof read. The
service rechecks every repository after evidence loading and evaluation, including
revocation during reads. It rejects expired output or clock rollback after final
admission. Source errors are sanitized. Trusted scheduled jobs must use canonical
stored actor/organization provenance, never message-provided role/edition claims.

The shared OpenAPI specification documents the manifest, attestation and result
schemas. No separate Enterprise schema, application, release or worker is created.

## Validation and remaining boundaries

Tests cover complete composition PASS, missing combined evidence despite component
PASS, connection/combined failures, expiry, UNKNOWN, replay against changed contracts
or policies, foreign components, input poisoning, canonical/immutable history,
resource limits, malformed JSON, cancellation and decoder fuzz seeds. Service tests
verify every repository is admitted before evidence access, quota admission,
permission/entitlement errors, legacy-key denial and revocation during reads.

Configuration integration tests cover all five editions, initial Business/
Enterprise admission, denial in other editions, configuration-only Team reassignment,
independent permissions after entitlement passes and preserved history after
downgrade. SQL tests compare embedded configuration with migration data and prohibit
implicit customer reassignment. Native migration fixtures verify sealed bundles,
copied mappings/limits and unchanged existing configuration/customer records.

This milestone provides the guard, service ports and additive provisioning data.
It does not implement automatic composition discovery, a contract-test generator,
HTTP/worker composition, a production evidence-source adapter or deployment
approval. Providers must execute combined verification through the shared core and
load its authoritative retained results. Native PostgreSQL integration still
requires an explicitly configured disposable database and `psql`. The v1.0 release
hold remains in force.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed after the final subject-collision regression fix. The shared
OpenAPI YAML and all three new schemas parsed successfully. A bounded manifest
decoder fuzz run passed with 55,889 executions. Configuration/migration parity
tests passed; `TestPostgresCompositionGuardMigration` was explicitly skipped
because the native PostgreSQL test environment was not configured. No migration
was applied to a deployment and no release was published.
