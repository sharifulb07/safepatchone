# Shared migration history

This is the single ordered PostgreSQL migration history used by all five editions. Never select migrations by edition or edit an already-applied migration. No migration runner is implemented yet.

`000001_editions.up.sql` creates permanent editions, versioned feature bundles, and the minimal feature-identity registry needed for foreign keys. It seeds metadata identities only; it assigns no features to any edition or customer. See the [edition model](../docs/architecture/edition-model.md).

`000002_initial_edition_bundles.up.sql` adds nine feature identities and publishes the five initial cumulative bundles (4/9/16/23/32 enabled mappings). It does not assign customers, activate feature implementations, or overwrite prior versions. See [initial edition bundles](../docs/architecture/initial-edition-bundles.md). Apply numbered migrations in order, once each.

`000003_entitlement_limits_quotas.up.sql` adds the eight stable resource keys, explicit effective limit policies and transactional quota counters/reservations. Missing policies deny admission. It seeds no numeric plan defaults and preserves edition history. See [entitlement limits and quotas](../docs/architecture/entitlement-limits-quotas.md). Native assertions are in `internal/platform/migrationtest/quota.sql`.

`000004_community_local_bundle.up.sql` registers `evidence.local` and publishes `community-2026-v2`, preserving the four existing Community grants. It does not change old bundles, customer assignments or quota data. The offline CLI compiles the same bundle; migration tests enforce parity. See [CommunityEntitlementProvider](../docs/architecture/community-entitlement-provider.md). Native fixtures are `community_bundle_before.sql` and `community_bundle.sql` in `internal/platform/migrationtest`.

`000005_evidence_graph.up.sql` adds shared immutable Evidence nodes and directed relationships, tenant-bound foreign keys, query indexes and append-only triggers. It preserves prior commercial and quota history. See [EvidenceGraph](../docs/architecture/evidence-graph.md); native assertions are `evidence_graph_before.sql` and `evidence_graph.sql` in `internal/platform/migrationtest`.

`000006_v02_edition_matrix.up.sql` adds two local-policy capability identities, five additive v0.2 bundles and immutable `edition_limits` provisioning templates. The provisional limit values and SDK/FUTURE metadata match the embedded configuration in `internal/config/editions_v02.json`. It does not assign customers or change effective limits/evidence. See the [v0.2 matrix](../docs/editions/v0.2-feature-matrix.md). Native fixtures are `editions_v02_before.sql` and `editions_v02.sql`.

Apply once to an empty intended schema using an appropriately privileged migration role and a trusted search path. Each migration includes its own transaction. Use libpq connection environment settings instead of embedding credentials in commands:

```powershell
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000001_editions.up.sql
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000002_initial_edition_bundles.up.sql
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000003_entitlement_limits_quotas.up.sql
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000004_community_local_bundle.up.sql
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000005_evidence_graph.up.sql
psql -X --no-password --set=ON_ERROR_STOP=1 --file=migrations/000006_v02_edition_matrix.up.sql
```

`000007_causal_change_graph.up.sql` extends the existing shared PostgreSQL graph adapter with revision-qualified entities, evidence-backed causal links, tenant/revision foreign keys, indexes and immutable-history triggers. It preserves existing evidence, editions and limits. See [Causal ChangeGraph](../docs/architecture/causal-change-graph.md); native assertions are `change_graph_before.sql` and `change_graph.sql` in `internal/platform/migrationtest`.

`000008_organization_entities.up.sql` adds organization-first SaaS entities and persistence for existing entitlement records. It binds the existing quota/graph tenant identities to inactive organization placeholders, preserves usage/history, enforces same-organization project and subscription references, and permits multiple organization memberships per user. See [organization entities](../docs/architecture/organization-entities.md). Native assertions are `organization_entities_before.sql` and `organization_entities.sql`.

No down migration is provided because deleting commercial history would violate grandfathering. Deploy additive corrections in subsequent numbered migrations. Existing releases must not silently replace configuration with an upsert.

For native integration tests, configure `PGHOST`, `PGPORT`, `PGDATABASE`, and a secure authentication mechanism (or `PGSERVICE`) for a test database and install `psql`. The test creates a unique schema, applies the migration and SQL assertions, checks that reapplication fails, and removes only that schema afterward:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgres -count=1 -v
```

Without explicit opt-in, the native integration test reports a skip; ordinary Go tests still validate SQL/domain identity consistency. The executable assertion suite is `internal/platform/migrationtest/editions.sql`. Run it only against a disposable migrated schema.

Initial-bundle tests run 000001, `initial_bundles_before.sql`, 000002, then `initial_bundles.sql` in a disposable schema. A separate conflict test runs `initial_bundles_conflict_before.sql` between the migrations, requires 000002 to fail, and checks rollback with `initial_bundles_conflict.sql`. All fixtures reside in `internal/platform/migrationtest`.

000009_subscription_sync.up.sql adds provider connection/customer bindings, durable webhook receipts, synchronized subscription observation state and immutable audit history. It preserves the shared subscriptions, entitlements and quota tables. See [Stripe subscriptions](../docs/architecture/stripe-subscriptions.md); native fixtures are subscription_sync_before.sql and subscription_sync.sql.

`000010_organization_trials.up.sql` adds immutable organization trials and tenant-bound associations to the existing entitlement ledger. It enforces one trial per organization, finite duration, sealed bundles, and matching grant dates/limits without rewriting legacy grants or paid subscriptions. See [organization trials](../docs/architecture/organization-trials.md); native fixtures are `organization_trials_before.sql` and `organization_trials.sql`.

`000011_onboarding.up.sql` adds hashed login sessions, encrypted provider credentials,
one-use OAuth attempts, revisioned organization onboarding, tenant-bound GitHub
connections and the durable first-proof outbox. It grants no entitlements and
preserves commercial history. See [onboarding](../docs/architecture/onboarding.md);
native fixtures are `onboarding_before.sql` and `onboarding.sql`.

`000012_github_pull_requests.up.sql` adds tenant-bound GitHub automation policies,
durable PR jobs and immutable webhook receipts. It grants no capabilities and
preserves subscription/edition history. See [GitHub App](../docs/architecture/github-app.md);
native assertions are `github_pr_before.sql` and `github_pr.sql`.

`000015_license_connectivity.up.sql` adds organization-owned signed-license
installation state, persistent confirmation timestamps and monotonic revisions.
It prevents installation deletion, revision rollback and confirmation rollback;
it does not rewrite edition, subscription, entitlement or usage history.
Native assertions are `license_connectivity_before.sql` and
`license_connectivity.sql`. See [license connectivity](../docs/architecture/license-connectivity.md).

`000016_shared_audit.up.sql` adds one tenant-bound immutable audit event table for
all editions. Premium export uses the same records as baseline capture/read.
No edition bundles, subscription state or entitlement history are changed.
Native assertions are `shared_audit_before.sql` and `shared_audit.sql`.

`000017_composition_guard.up.sql` registers `feat_0042` / `proof.composition`
and publishes sealed `business-2026-v3` and `enterprise-2026-v3` bundles by copying
the v2 mappings/limits and adding the new grant. It preserves `verify.composition`
and all historical customer assignments. Provisioners explicitly select the new
bundles using `config.CompositionEditions`; existing SaaS offers are not silently
reassigned. Go/SQL configuration parity is tested. Native fixtures are
`composition_guard_before.sql` and `composition_guard.sql`; run
`TestPostgresCompositionGuardMigration` with the disposable PostgreSQL opt-in above.
See [CompositionGuard](../docs/architecture/composition-guard.md).

## GlobalPatch capability

`000018_global_patch.up.sql` adds `feat_0043` / `graph.global_change` and publishes
the sealed `enterprise-2026-v4` bundle, inheriting the existing v3 features/limits.
It does not reassign subscriptions or rewrite entitlement/usage history. Select
`config.GlobalPatchEditions` explicitly during provisioning. GlobalPatch artifacts
reuse immutable evidence storage, so no new artifact table is needed. SQL/config
parity and native migration fixtures live in `internal/platform/migrationtest`
(`TestPostgresGlobalPatchMigration`).

## Safe upgrade capability and monthly usage

`000019_safe_upgrade.up.sql` adds `automation.safe_upgrade` and the shared
`safe_upgrades_monthly` quota key. It publishes four additive bundles with provisional
Developer/Team/Business limits of 10/100/1000 and Enterprise unlimited. It extends
the existing monthly usage constraint/non-refund trigger without changing customer
assignments or prior history. Provision explicitly with `config.SafeUpgradeEditions`.
See [safe upgrades](../docs/architecture/safe-upgrade.md) and
`TestPostgresSafeUpgradeMigration` for native validation fixtures.

`000020_earned_autonomy.up.sql` validates proof bindings in existing SafeLease
records and adds the immutable `enterprise-2026-v6` provisioning bundle. Existing
leases and customer assignments are preserved. See
[earned autonomy](../docs/architecture/earned-autonomy.md) and the opt-in
`TestPostgresEarnedAutonomyMigration` fixture.

`000021_evidence_quorum.up.sql` registers `feat_0046` / `proof.quorum` without
changing existing edition bundles or organization grants. Quorum assessments and
relationships use the existing immutable EvidenceGraph tables. See
[EvidenceQuorum](../docs/architecture/evidence-quorum.md) and the opt-in
`TestPostgresEvidenceQuorumMigration` fixture.
