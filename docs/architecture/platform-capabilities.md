# Enterprise capabilities in the shared platform

The five existing stable feature keys now control application services through
the same EntitlementService, PermissionService, tenant resolver and guards used
by ordinary operations. No catalog keys, edition bundles or edition-specific
applications are added. Version, edition version and release channel stay separate.

| Operation | Capability | Independent permission |
| --- | --- | --- |
| Organization SSO authentication | `enterprise.sso` | `proof.view` and active membership |
| Audit export | `enterprise.audit` | `organization.manage` |
| Customer-managed verification | `enterprise.self_hosted` | `proof.execute` |
| Air-gapped verification | `enterprise.self_hosted` and `enterprise.airgap` | `proof.execute` |
| LTS support eligibility | `enterprise.lts` | `organization.manage` |

`PlatformAccess` receives trusted dependencies at composition. Resource ownership
and current membership are resolved before capability/permission checks. Errors,
unknown capabilities, cancellations and cross-tenant results fail closed. A
commercial grant never supplies actor roles or production approval.

## Shared identity

`NewSSOService` binds the existing Authenticator interface to one organization.
The identity adapter must validate the chosen federation protocol, issuer,
audience, expiry and replay protection, and resolve an explicitly linked existing
user. The service then uses GuardedOperation for current membership, entitlement,
permission and quota checks. Service accounts are rejected as SSO users.

The result contains the existing Principal and organization ID. It does not create
another user table, auto-link by email, assign organization roles, or create a
global session. The shared session/request adapter must retain the organization
binding and recheck access on each use. An SSO authentication result is not a
durable authorization lease. This milestone implements the capability boundary;
it does not introduce an OIDC/SAML implementation or an HTTP login callback.
Identity-provider protocol setup and session integration remain adapter work.

## Shared audit

`audit.Event` and `audit.Store` define one baseline stream for every edition.
Events have bounded identifiers, shared actor identity, action, resource, outcome
and time. They contain no arbitrary prompt/scanner output or credential field.
Only trusted services can append; append is not premium-gated. PostgreSQL migration
000016 creates tenant-bound immutable `audit_events`. It rejects projection/tenant
mismatches, updates, deletes and truncation. Duplicate event IDs conflict.

`AuditService.Read` uses `audit.organization` plus `audit.view`.
`AuditService.Export` uses `enterprise.audit` plus `organization.manage`; owners
may export while viewers cannot. Both read exactly the same records. Export
returns at most 200 ordered events, a continuation cursor and a SHA-256 digest
bound to schema, organization and page. The digest is not a signature or proof
that all events have been captured. Pagination is by immutable event ID, not a
transactional snapshot or chronological feed. Large complete exports require an
operator-controlled snapshot/export adapter, not unbounded in-memory collection.

Supply the store as `ChangeWorkflowConfig.Audit` for baseline proof-operation
capture. The same hook records both local and hosted execution and admission
failures, even without `enterprise.audit`. Capture failure prevents returning PASS;
the workflow returns UNKNOWN with an audit error. The operation outcome is not
the verification verdict. Existing subsystem-specific billing/security histories
are preserved; this milestone does not silently copy or migrate them.

Downgrades do not delete events or change their schema. No retention purge or
arbitrary payload export is added. The database writer remains a trusted internal
role; do not expose Store.Append directly as a user/scanner endpoint.

## Shared deployment and release services

Customer-managed composition supplies `NewDeploymentAccess(access, airGapped)`
as `ChangeWorkflowConfig.Deployment`. It checks both capability and actor access
before core execution, and repeats the checks at actual scanner dispatch. The
same ChangeWorkflow, registry, runners, policy, integrity guards, evidence and
Passport code execute afterward. Existing verification and step guards continue
to enforce their own capabilities and quotas; deployment checks do not reserve
verification capacity a second time. No grant upgrade changes completed proofs.

Combine this with the existing signed-license provider and
[connected/offline policy](license-connectivity.md). Air-gap licensing still has
no phone-home requirement. The deployment adapter must supply offline-capable
sources and runners and enforce network isolation; a feature grant alone cannot
disconnect a network. Ordinary Community-compatible local execution retains its
existing composition without a customer-managed platform gate.

`ReleaseService` holds one bounded trusted catalog of released `major.minor.patch`
versions, stable/beta/nightly/lts channel labels and support deadlines. Historical
`LTS` input is normalized to `lts`. See [release channels](release-channels.md) for
shared artifact delivery and compatibility. Eligibility is checked
against current entitlement and permission; unknown releases and expired support
fail closed. The same version may have both labels. This service does not install
software, select an edition, rewrite subscriptions or automatically switch channels
on downgrade. An actual signed release downloader/updater is a separate adapter.

## Validation and file report

Tests cover entitled and non-entitled access for every requested capability,
independent active membership/permissions, downgrade/restore preservation,
SSO unverified/service-account rejection, audit poisoning, bounded pagination,
cancelled/error paths, expired release support and tenant-scoped SQL.
Integration tests compare actual local and customer-managed workflow artifacts,
check baseline audit without premium grants, reject audit failures, and revoke a
deployment grant between admission and scanner dispatch. Native SQL assertions
check audit immutability and preservation of commercial history.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
# Optional native PostgreSQL check after configuring the disposable test DB:
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresSharedAuditMigration -count=1 -v
```

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed on the final code. The shared audit migration contract test
passed. `TestPostgresSharedAuditMigration` explicitly skipped because the opt-in
PostgreSQL/libpq environment was not configured; native SQL execution is not
claimed as validated. No live identity-provider or production deployment changes
were performed.

Inspected: application onboarding/guard/workflow/integrity code, authorization
service/permissions, feature and edition catalogs, runtime boundary, shared
GitHub/session authentication, licensing modes, audit boundary, SQL store/test
conventions, workflow integration fixtures, migrations and OpenAPI.

Created: `internal/application/platform_capabilities{,_test}.go`,
`internal/audit/event{,_test}.go`,
`internal/platform/auditstore/postgres{,_test}.go`,
`internal/platform/githubapp/platform_capabilities_integration_test.go`,
`internal/platform/migrationtest/shared_audit_test.go`,
`internal/platform/migrationtest/shared_audit{,_before}.sql`,
`migrations/000016_shared_audit.up.sql`, and this guide.

Modified: `internal/application/change_workflow.go`, `internal/audit/doc.go`,
`api/openapi/openapi.yaml` and `migrations/README.md`. No dependencies changed.

Remaining composition work: trusted IdP protocol/session integration, exposing
these services through authenticated API routes, wiring baseline audit and
deployment gates into host bootstrap, and validating native PostgreSQL. Existing
hosts are not silently reconfigured and no new federation protocol is advertised
as implemented by the capability boundary.

Next prompt: "Wire the shared capability services into authenticated host routes
and bootstrap, integrate an organization-bound identity provider, and validate
native PostgreSQL audit persistence end to end."
