# Organization-first SaaS entities

SafePatch uses one organization-owned SaaS model across all five editions. The
shared Go records live in `internal/domain/organization`; PostgreSQL persistence
is in `internal/platform/organizationstore`. Existing authorization memberships,
canonical Entitlement records and quota accounting are reused.

| Entity | Identity and ownership |
| --- | --- |
| User | Global `user_id`; display name, active state and creation time |
| Organization | `organization_id`; name and PENDING/ACTIVE/SUSPENDED state |
| Organization member | `(organization_id, user_id)`; independent owner/viewer role and active state |
| Project | `(organization_id, project_id)` |
| Repository | `(organization_id, repository_id)`; same-organization project foreign key |
| Subscription | `(organization_id, subscription_id)`; sealed edition-bundle reference |
| Entitlement | Existing `(organization_id, entitlement_id)` record and feature reference |
| Usage | Existing `(organization_id, limit_key, period)` quota ledger |

A user can belong to several organizations, with a different role in each. There
is no user-level main subscription or repository-level subscription field. Project
and repository IDs may recur in different organizations without sharing ownership.
Repository/project IDs cannot equal their organization ID, which is reserved for
organization-level operation scopes. Repository validation also rejects that
reserved ID as its parent project ID, both before writes and when reading stored
records; the project constraint and composite foreign key enforce the same rule
in PostgreSQL.

## Subscriptions, entitlements and usage

Subscriptions pin an immutable sealed `edition_version_id`, independently of the
software version and release channel. Provider and external reference are bounded
opaque identifiers, not credentials. `(provider, external_reference)` is unique to
prevent attaching the same external subscription to multiple organizations.

Each organization has at most one current main subscription across PENDING,
TRIALING, ACTIVE and PAST_DUE states. CANCELED and EXPIRED records remain stored;
a replacement may then be created. Revision-checked updates prevent stale writes
and cannot move organization ownership, replace provider identity or move creation
time. Changing a bundle or subscription status does not rewrite existing grants,
memberships, repositories, evidence or usage. Billing transition validation and
audit-event ingestion remain responsibilities of authenticated billing workflows.

The new `entitlements` table persists the existing canonical Go Entitlement schema.
It implements the existing `EntitlementRepository.Create/Get/List` contract, including
bounded keyset pagination and disabled/future/expired records. IDs are unique within
an organization. Writes reject duplicates; updates, deletes and truncation are
blocked. Read boundaries validate canonical data and its indexed organization,
feature and source fields. Original timestamps retain nanosecond precision in JSON.

For `source=SUBSCRIPTION`, `source_reference` is the internal subscription ID. A
generated reference column and composite foreign key enforce that the subscription
belongs to the entitlement's organization. Other sources remain canonical opaque
references validated by their existing issuer/resolver boundaries. Do not store a
Stripe customer ID, repository ID or unverified payment claim as a subscription grant
reference. Only trusted issuance can create grants.

Subscription metadata itself grants no feature access. The existing resolver,
issuer validation, expiration/restriction rules and PermissionService remain
authoritative. Cancellation/revocation must be reflected through trusted entitlement
source resolution; changing this metadata row alone is not that workflow.

Usage remains `quota_usage`, protected by the existing organization-scoped quota
lock and Reserve/Commit/Release operations. There is no second usage counter or
plan-specific accounting table. `quota_accounts` now references `organizations`,
so quota adapters can no longer implicitly create an unknown organization. Create
or reconcile the organization before provisioning its effective limits.

## Persistence and access boundaries

`organizationstore.NewPostgres(db, featureCatalog)` provides the entity store,
existing MembershipRepository and EntitlementRepository, and tenant resolution.
It accepts an injected primary database connection. Composition owns driver
registration, TLS, credentials, a trusted fixed search path and request deadlines.

Entity Create/Get methods bind explicit organization IDs for projects, repositories
and subscriptions. Subscription updates use compare-and-swap revision and immutable
provider/creation fields. Membership creation/update uses the existing
`authorization.Membership` and its finite owner/viewer roles. Read queries reject
invalid or mismatched returned data. Database errors must pass through existing
application stage redaction before exposure to a client.

These are trusted persistence interfaces, not public authorization entrypoints.
Provisioning writers require verified identity and organization-management authority;
billing writers require authenticated billing provenance and appropriate permission.
They must also reserve repository/developer limits where applicable. A caller must
never be allowed to create a paid subscription or grant by supplying these DTOs to
an unguarded API. No new account, billing or invitation endpoint is exposed here.

`GetMembership` joins the member, user and organization. A suspended/pending
organization, disabled user or inactive membership returns no effective active role.
It can be injected directly into the existing PermissionService, which checks
permissions independently from feature ownership. A service-account principal is
not treated as a same-named user; this milestone supports user memberships only.

`Resolve` implements the existing application TenantResolver for organization and
repository scopes. It reloads membership, then checks active repository/project
ownership using organization-qualified joins. Organization management uses the
organization ID as ResourceID. Project management uses an organization-authorized
operation plus scoped GetProject. Every operation/worker retry must reauthorize;
returned scopes and membership records are not durable leases.

`ListOrganizations` uses the authenticated user principal, returning only active
memberships/organizations with bounded C-collated keyset pagination. User IDs and
principals must come from authentication, not another user's query parameter.
Global GetUser is for trusted identity/provisioning code, not public user enumeration.

Entity ownership and creation identity are immutable in PostgreSQL. Soft suspension
and membership deactivation preserve history. Trusted maintenance roles are required
for any later deletion/purge workflow. These tables do not implement PostgreSQL RLS
or defend against administrators disabling triggers. Give ordinary service roles
only the permissions needed by their workflows; scanners receive no DB credentials.

## Additive migration

Apply `000008_organization_entities.up.sql` after 000001–000007. It adds the missing
entity tables, entitlement persistence, indexes, foreign keys and identity guards.
Earlier numbered migrations remain unchanged.

Existing quota and graph tenant IDs are registered as **PENDING placeholders**.
Their names and creation time describe migration registration, not verified business
identity. No users, memberships, active subscriptions or grants are synthesized.
Existing quota counters, reservations, evidence and edition history are preserved.
Trusted provisioning must verify ownership, reconcile metadata and explicitly
activate these organizations. Historical graph repository references are not guessed
into new project/repository records; ownership must be reconciled separately before
the new tenant resolver allows access to them.

New organization foreign keys bind quota accounts and both graph node tables to the
shared organization registry. Graph edges and quota buckets inherit ownership through
their existing composite foreign keys. No new commercial bundle or edition-specific
migration stream is created.

## Validation and remaining scope

Tests cover canonical entities, independent multi-organization roles, real
PermissionService/ExecutionGuard integration, entitlement denial after tenant
resolution, repository isolation, disabled memberships, subscription CAS, malicious
metadata, list pagination and backend errors. Existing quota and migration tests
remain part of the full suite.

Native migration assertions test fresh application and legacy backfill, same-user
membership in two organizations, same IDs in different tenants, cross-organization
project/subscription rejection, single current subscription, immutable ownership,
plan changes and preservation of usage/evidence/bundles. With psql and a disposable
database configured through libpq settings:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresOrganizationEntitiesMigration -count=1 -v
```

Without explicit configuration native tests skip. Scripted database/sql transport
tests do not validate PostgreSQL SQL execution or locking. Run `go test ./...`,
`go test -race ./...` and `go vet ./...` for shared validation.

Authentication credentials, external identity mapping, invitations, service-account
storage, last-owner transfer rules, billing checkout/webhooks, snapshot-source
persistence, production API/worker wiring and data-deletion workflows are outside
this entity milestone. The offline Community provider remains account-free.
