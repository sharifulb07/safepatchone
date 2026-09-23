# Connected and air-gapped license availability

Both modes use `EnterpriseLicenseEntitlementProvider`, the same signed document,
the shared entitlement resolver, quota ledger and verification engine. Deployment
mode is trusted operator configuration; edition labels grant nothing. Connected
mode requires signed `enterprise.self_hosted`. Air-gapped mode also requires
signed `enterprise.airgap`. The provider checks these through the shared resolver
on every admission, including when resolving resource limits.

## Outage policy

Connected configuration supplies a refresh interval (one minute to 24 hours) and
a grace period (zero to 30 days). A practical starting configuration is a one-hour
refresh interval with 24 hours of grace; deployments must choose their own values.
Zero grace explicitly disables the overdue window. Negative, oversized and unknown
configurations fail validation. There is no new commercial edition or trial mode.

For a persisted `confirmed_at` of Monday 12:00, a one-hour interval and 24-hour
grace, connectivity is current until Monday 13:00, then in grace until Tuesday
13:00. New admissions stop at that exact deadline if no successful refresh has
been persisted. The deadline is always capped by the signed `expires_at`.

The formula is `min(expires_at, confirmed_at + refresh_interval + grace_period)`.
This is grace for a delayed connectivity check, not an extension of an expired
contract. A restart, failed request, retry or scheduler delay does not move the
confirmation anchor. Scheduling failure is treated like an overdue status check
and receives the same bounded window. Successful authenticated status refresh
with a currently valid signed envelope establishes a new anchor.

The local PostgreSQL installation store remains available during an Internet
outage. Entitlement reads never call safepatch.dev. They reload local state and
verify signature, tenant, capabilities and time. Database failure, malformed or
forged license, expired signature grants, missing anchor, explicit denial and
revocation do not qualify for grace. A confirmed denial is persisted before the
refresh completes and takes effect on subsequent admissions across nodes.
Failed persistence is an operational error and must be alerted/retried; only
committed installation state is authoritative to other processes.

`Availability` exposes `current`, `grace` or `offline`, refresh due time and the
effective validity deadline. `Resolution.Expiration` is capped at that deadline;
as with other resolver outputs it is not a cache permission. No connectivity
status turns UNKNOWN verification into PASS or bypasses PermissionService,
VerificationIntegrity, PolicyGuard or SelfApprovalGuard.

## Air-gapped operation

Configure `DeploymentPolicy{Mode: AirGapped}` with zero refresh/grace durations.
Install the signed envelope and public keys through the trusted offline operator
process. No confirmation timestamp or network client is required. License checks
continue until the signed expiry, with local revocation taking effect immediately.
`NewLicenseRefresher` rejects air-gapped mode even if a client is supplied. There
is no telemetry or phone-home requirement in this licensing path.

## Connected composition

Apply the shared migration `000015_license_connectivity.up.sql`. Construct
`licensestore.NewPostgres(db, organizationID)` and pass that same store to both
the provider and refresher. The store is bound to one configured organization.
All SQL is parameterized; revisions use compare-and-swap to prevent a late
refresh overwriting an operator installation or revocation.

The trusted installer verifies the envelope with the base signed-license provider
before inserting revision 1 through `CompareAndSwapLicense`. For a connected
installation it records the actual installation time in `ConfirmedAt` as the
initial offline activation anchor. Do not advance this timestamp merely on process
startup or reinstallation of unchanged state. Replacement, removal by revocation
and refresh atomically increment revision. This metadata must never be accepted
from repository content, browser input or an unsigned license attachment.

```go
policy := licensing.DeploymentPolicy{
    Mode: licensing.Connected,
    RefreshInterval: time.Hour,
    GracePeriod: 24 * time.Hour,
}
// config also contains the configured organization, trusted public keys,
// immutable catalogs and clock from the signed-license composition.
config.Source = store
config.Deployment = policy
provider, err := licensing.NewEnterpriseLicenseEntitlementProvider(config)
// Handle err. Inject provider into existing ExecutionGuard and LicensedQuotaStore.
refresh, err := licensing.NewLicenseRefresher(licensing.RefreshConfig{
    Provider: config, Store: store, Client: licenseclient.New(licenseclient.Config{}),
    RetryInterval: time.Minute, Timeout: 5 * time.Second,
    Observe: observeRefreshOutcome,
})
// Handle err. Supervise refresh.Run(ctx) as a background worker owned by shutdown.
```

`Run` attempts an overdue check immediately, then polls at the configured retry
interval. Fresh installations do not make unnecessary requests. `RefreshOnce`
also supports an external scheduler. Context deadlines bound remote waits;
verification admission continues while refresh is waiting. The observation hook
receives sanitized outcomes for deployment logging/metrics. No upstream body or
connection credential is included in errors.

## Connected issuer transport contract

The optional adapter posts only organization ID, license ID and a cryptographically
random request nonce to `https://safepatch.dev/api/v1/licenses/status`. It uses
verified HTTPS, fixed destination, bounded time/body/connections, no environment
proxy and no redirects. A response must echo the exact request identity and nonce.
Active status must contain a signed envelope, verified independently by the
existing provider verifier before installation. Renewal can replace an expired
license; expired data is never used to authorize work during renewal.

DNS/network unavailability, timeout, HTTP 408/429 and 5xx leave persisted state
unchanged and can consume the existing grace window. Certificate validation
failure, 401/403, redirects, unknown status, malformed or mismatched responses,
invalid signatures and explicit denied/revoked status fence access. Later network
errors cannot clear that denial. Recovery from denial requires a valid authoritative
active response or trusted operator installation with a newer revision. Revocation
is sticky: refresh stops until a trusted operator explicitly replaces that state.
An online response cannot undo a local operator revocation.

This is a versioned outbound integration contract, represented as components in
the one OpenAPI document. The remote safepatch.dev issuer handler is not deployed
by this repository change. Mock transport tests exercise the contract; live issuer
compatibility must be verified before enabling the connected adapter in production.
Air-gapped execution does not depend on that service existing.

## Persistence, tests and limits

Migration 000015 adds `license_installations` only. Its organization foreign key,
immutable identity, strictly increasing revisions, nondecreasing confirmation
timestamps and delete/truncate protection retain the grace anchor across restarts.
The Go provider additionally rejects observed clock/revision rollback. A trusted
clock and protected database remain required; restoring a complete old database
backup or altering operator policy is outside the database trigger boundary.
Key rotation remains operator-owned; self-hosted nodes contain public verification
keys only. No private license-signing operation is introduced.

Tests cover connected outage/recovery, exact grace/expiry boundaries, restart,
air-gap no-network behavior, missing capabilities, independent authorization
during grace, quota history under downgrade, cancellation, concurrent revocation,
HTTP status/error classification, response poisoning and SQL isolation/CAS errors.
Native migration assertions cover revision/timestamp rollback, tenant identity,
delete/truncate, stale refresh and preservation of existing commercial state.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
# With an explicitly configured disposable PostgreSQL/libpq environment:
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresLicenseConnectivityMigration -count=1 -v
```

The CLI/server bootstrap is not automatically switched from its existing provider.
The operator installation API/CLI, remote issuer handler and deployment process
supervision remain integration boundaries; this milestone supplies the reusable
mode policy, refresh worker, transport and durable state adapter. Existing
reservations can still settle/release after grace expires; new work must always
pass the shared guard immediately before execution.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Focused licensing, HTTP transport, SQL adapter and migration
contract tests passed. `TestPostgresLicenseConnectivityMigration` explicitly
skipped because the opt-in PostgreSQL/libpq environment was not configured.
No live requests were sent to safepatch.dev.

Inspected files: `internal/licensing/{provider,document,quota,provider_test,doc}.go`,
`internal/entitlement/resolver.go`, `internal/domain/feature/catalog.go`,
`internal/platform/organizationstore/entitlements.go`,
`internal/platform/quotastore/postgres_test.go`,
`internal/platform/githubapp/{client,client_test}.go`,
`internal/platform/architecture/{architecture_test,license_keys_test}.go`,
`internal/platform/migrationtest/{editions_test,replay_test}.go`, the replay SQL
fixtures, migrations 000001/000014 and README, the license guide and shared OpenAPI.

Created files: `internal/licensing/{deployment,refresh,deployment_test}.go`,
`internal/platform/licenseclient/{http,http_test}.go`,
`internal/platform/licensestore/{postgres,postgres_test}.go`,
`internal/platform/migrationtest/license_connectivity_test.go`,
`internal/platform/migrationtest/license_connectivity{,_before}.sql`,
`migrations/000015_license_connectivity.up.sql`, and this guide.

Modified files: `internal/licensing/{provider,document,doc}.go`,
`api/openapi/openapi.yaml`, `migrations/README.md` and
`docs/architecture/enterprise-license.md`. No dependencies or edition bundles changed.

Next prompt: "Wire the connected and air-gapped license composition into trusted
self-hosted bootstrap, add an operator-only signed-license install command, and
validate the deployed issuer contract and PostgreSQL migration end to end."
