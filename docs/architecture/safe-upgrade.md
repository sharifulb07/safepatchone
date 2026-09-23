# Verified dependency upgrades

`application.SafeUpgradeService.Verify` verifies an immutable prepared dependency
upgrade through the existing concrete `ChangeWorkflow.RunHosted`. All editions use
the same integrity guard, sandbox admission, Intent, SIR, graph, ProofPlan, verifier,
policy, evidence store and Passport generation. No feature flag bypasses baseline
security checks. The service returns PASS, FAIL or UNKNOWN; PASS is a recorded
verification outcome under the configured policy, never approval to merge/deploy.

## Candidate identity and source contract

`verification/upgrade.Candidate` pins the existing organization/repository Change,
exact from/to dependency versions, ecosystem/name and all changed manifest/lockfile
paths with before/after content digests. The manifest accepts 1-32 distinct files
and at most 256 KiB. No-op versions, commits or file digests; unsafe paths;
shell-like names; ranges; malformed IDs; duplicate/unknown JSON fields; and trailing
documents are rejected. Canonical file order makes the `upgrade_` SHA-256 identity
stable across reordering. The hash covers the schema prefix, newline and canonical
JSON. Software version and edition are absent from the identity and schema.

The injected `UpgradeSource` loads server-owned candidates by ID. It must inspect
both exact revisions, confirm that declared dependency versions match manifest and
lockfile contents, include all changed files, and reject unrelated edits/incomplete
extraction. Candidate schema validation alone cannot establish those facts. Source
adapters must never execute package install hooks, fetch caller-provided URLs,
resolve mutable tags during verification or accept scanner/LLM prose as authority.

`IsCurrent` confirms repository ownership/current intended base and availability of
the exact candidate revisions before and after verification. Moving branches,
revoked candidates or source failures return UNKNOWN with no successful result.
Both the candidate source and the workflow source must refer to the same immutable
revision contents. Registry resolution and candidate preparation are separate from
this verification service; the ecosystem field is schema support, not a claim that
all six ecosystems have production candidate-source adapters.

## Entitlements, permissions and quotas

The service first resolves the tenant and checks `automation.safe_upgrade` plus
independent `proof.execute` permission through the shared ExecutionGuard. It loads
and checks the candidate, refreshes access, atomically charges the monthly attempt,
then runs the shared workflow. It refreshes candidate state and access before
returning. The workflow also enforces its existing verifier capabilities and quotas.
Source/workflow errors are sanitized and cannot yield PASS.

Migration 000019 and `config.SafeUpgradeEditions` publish matching, additive sealed
bundles. Initial **provisional provisioning defaults** are:

| Edition | New bundle | automation.safe_upgrade | safe_upgrades_monthly |
| --- | --- | --- | --- |
| Community | Existing bundle | Disabled | Missing policy denies |
| Developer | developer-2026-v3 | Enabled | 10 |
| Team | team-2026-v3 | Enabled | 100 |
| Business | business-2026-v4 | Enabled | 1000 |
| Enterprise | enterprise-2026-v5 | Enabled | UNLIMITED |

These are configuration defaults, not runtime edition comparisons or approved
pricing promises. Provisioners must explicitly pin a new bundle and install its
resource policies in the authoritative organization quota store. Existing SaaS
offers, subscriptions, signed licenses and historical grants remain pinned.
Custom caps and future reassignment require only new bundle data/provisioning;
the execution code remains identical. Unlimited removes the commercial count cap,
not sandbox budgets, concurrency limits, permissions or storage safety bounds.

`QuotaService.ConsumeOnce` uses the existing organization-serialized quota ledger
to atomically check policy, increment committed monthly usage and create a durable
attempt identity derived from candidate ID and run ID. Duplicate deliveries fail
before executing verification again. Failed/UNKNOWN runs and crashes after the
claim consume an attempt; monthly usage cannot be refunded. Retrying after a crash
requires a new run ID and another attempt. This is an at-most-once execution claim,
not a queue/result recovery system. Production must use the PostgreSQL quota store;
the bounded memory adapter is for tests/embedded use only.

Missing/disabled limits deny access. UTC month rollover creates a new accounting
bucket without erasing old usage or duplicate IDs. Downgrades preserve reservations,
candidate identities, evidence and Passports. The schema migration extends the
existing period constraint and non-refund trigger while retaining prior resource
protections; no parallel billing or quota database is introduced.

## Validation and integration boundary

Unit and integration tests cover immutable candidates, malformed/injected input,
all five editions using the actual shared workflow with fixture execution, permission
denial after entitlement, quota limits/customization, concurrent duplicate claims,
month rollover, downgrade/history preservation, stale base, revoked access, sandbox/
integrity errors, UNKNOWN and archive errors. SQL/config parity tests and native
PostgreSQL fixtures cover allocation, preserved history, monthly period constraints,
sealed bundles and non-refundable attempts. Native tests require
`SAFEPATCH_POSTGRES_MIGRATION_TEST=1`, `psql` and disposable libpq connection settings.

This milestone implements verification of prepared upgrades. Production registry/
manifest candidate adapters, package-manager edits, PR creation, HTTP/queue wiring,
result recovery and automatic merging/deployment are not implemented here. Candidate
source implementations are mandatory at composition time. The shared API contains
candidate/result schemas; no unimplemented HTTP endpoint is advertised. Existing
release blockers remain in force.

Validation on 2026-09-07:

- `go test ./...`: passed.
- `go test -race ./...`: failed in the existing protected-download boundary test
  at `downloads_test.go:141` with HTTP 503 `DOWNLOAD_UNAVAILABLE`. The failing case
  was Team/airgap/enterprise.self_hosted. This matches documented release blocker
  B01 in `docs/releases/v1.0-readiness.md`; no data-race diagnostic was emitted.
- Three focused repetitions of that downloads test also failed, this time for
  Community/host/enterprise.self_hosted. The failure remains unresolved.
- Focused race tests for upgrade candidates, shared workflow integration, quota
  storage, entitlement services, configuration and migrations passed.
- `go vet ./...` and shared OpenAPI YAML/schema parsing passed.
- SQL/config parity passed. `TestPostgresSafeUpgradeMigration` was skipped because
  the native PostgreSQL environment was not configured. No migration was applied
  to a deployment, no release was made, and the v1.0 release hold remains.
