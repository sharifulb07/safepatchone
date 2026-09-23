# Organization trials

Organization trials publish ordinary `TRIAL` records in the existing entitlement
ledger. They use SafePatch edition configuration, independently of Stripe and
product release versions. There is no trial flag in verification or policy code.

| Offer | Duration | Initial sealed bundle |
| --- | --- | --- |
| Developer | 14 consecutive 24-hour days | `developer-2026-v2` |
| Team | 14 consecutive 24-hour days | `team-2026-v2` |
| Business | Configured integer, 1–365 days | `business-2026-v2` |

Business is unavailable until both its bundle and duration are configured.
Community and Enterprise are not trial offers. Edition checks above validate
commercial configuration; operations continue to use capabilities and permissions.

## Composition

Apply the shared migration history through `000010_organization_trials.up.sql`.
Supply an existing `*sql.DB`, feature catalog, and trusted UTC clock. A sample
composition with a 30-day Business offer is:

```go
policy, err := trial.NewPolicy(
    "developer-2026-v2", "team-2026-v2", "business-2026-v2", 30,
)
if err != nil { return err }
management, err := trialstore.NewManagement(db, policy, features, time.Now)
if err != nil { return err }
runtime, err := billingstore.NewOrganizationRuntime(
    db, features, "community-2026-v3", time.Now,
)
if err != nil { return err }
```

Pass `runtime` to existing entitlement/operation guards. If Stripe subscriptions
are already composed, their `Entitlements` service reads these trial grants too;
no additional runtime is needed. `NewOrganizationRuntime` requires no provider
configuration or credentials and never contacts Stripe.

After trusted authentication, use
`management.Start(ctx, principal, organizationID, edition.Team)` and
`management.Get(ctx, principal, organizationID)`. These are Go application entry
points; this milestone does not add an HTTP endpoint or start the API command
shell. Both calls reload tenant scope and require `billing.manage`. A user can be
an owner in one organization and a viewer in another. Starting a trial does not
require a premium capability; executing premium work still requires both its
entitlement and its independent permission, plus existing quota checks.

## Lifetime and retries

The initial eligibility policy is **one lifetime trial per organization across
all offers**. An organization row lock serializes competing starts. A database
primary key prevents a second trial. A repeated request for the same edition
returns the original record, including after expiry or a server offer change.
Selecting a different edition returns `trial.ErrUsed`. No retry extends access.
Existing legacy `TRIAL` grants also consume eligibility, even if expired.

Only trusted server policy selects the sealed edition version and duration. The
start operation captures its clock after acquiring the organization lock. It
writes the immutable trial, canonical grants, and tenant-bound grant associations
in one transaction. Failure rolls everything back. Policy changes affect only
new trials; existing dates, bundles and evidence remain immutable.

## Expiration and other sources

Every grant carries the same inclusive `starts_at` and exclusive `expires_at`.
The existing resolver removes expired grants from each access decision. At the
exact expiry instant, the organization uses its configured Community fallback
plus any other applicable source. Active subscriptions and enterprise licenses
retain their existing precedence; manual restrictions still deny access. Trial
expiry does not cancel subscriptions, overwrite licensed grants, remove project
or repository data, or reset quota usage/reservations. It does not interrupt work
that was already admitted; sensitive execution boundaries should recheck access.

No scheduler, webhook, cleanup mutation, or restart is required for expiration.
The database runtime reads current state without a cross-request cache. Other
runtime compositions must preserve canonical grant expiry and the existing
snapshot expiration/revision contracts. This milestone does not add a snapshot
source adapter for trial writes.

Trials copy feature grants and per-feature limits from sealed bundles. They do
not invent numeric resource budgets or overwrite `quota_limits`; existing
resource provisioning remains required, and missing quota policies deny access.

## Validation and deployment boundary

Unit and application tests cover fixed/configurable offers, invalid identities
and dates, independent permissions, tenant mismatch, cancellation and redacted
errors. Scripted SQL integration tests exercise atomic writes/rollback, retries,
legacy eligibility, sealed bundles, commit failures and the shared database
runtime. They do not prove native PostgreSQL locking.

Native fixtures apply all ten migrations, preserve prior subscriptions, legacy
entitlements, memberships, repositories and usage, and assert tenant-bound grant
associations, duration constraints, immutability, duplicate rejection and rollback:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresOrganizationTrialsMigration -count=1 -v
```

Provide `psql` and libpq settings for a disposable test database. The harness
creates and removes only its own unique schema. Without explicit opt-in this
native test skips; passing ordinary Go/race tests is not live database validation.

No database was migrated or HTTP listener deployed by this milestone. Trusted
composition must use authenticated principals, maintain an accurate clock, and
keep raw persistence ports out of public handlers. Cancellation/extension flows,
trial email reminders, and controls against creating many organizations are not
implemented in this milestone.
