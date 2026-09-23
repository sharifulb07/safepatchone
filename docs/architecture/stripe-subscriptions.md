# Stripe subscription synchronization

SafePatch uses Stripe for billing input and its own PostgreSQL subscription state
and sealed edition configuration for runtime entitlement decisions. The shared
`BillingProvider`, organization model, resolver, permissions and quota ledger are
reused. No Stripe entitlement API is queried or imported into core code.

## Products and local configuration

`stripe.SubscriptionProducts` defines these merchandising mappings:

| Stripe product name | SafePatch offer | Pinned local bundle |
| --- | --- | --- |
| SafePatch Developer | `developer_subscription` | `developer-2026-v2` |
| SafePatch Team | `team_subscription` | `team-2026-v2` |
| SafePatch Business | `business_subscription` | `business-2026-v2` |

Create the three products and recurring prices in the intended Stripe account,
then supply their real `price_...` IDs to the helper. No account credentials,
amounts, currencies, or live product IDs are invented or created by tests.
`ProductOffers` converts the mapping for the existing checkout adapter. Retain
historical offer mappings while existing subscriptions reference those prices.
The local catalog must contain each pinned version. Enterprise remains managed
through trusted contracts/license issuance; no Enterprise checkout is added.
Product software version and release channel are independent of these bundles.

## Composition

Apply migration `000009_subscription_sync.up.sql` after 000001–000008. Construct
`billingstore.NewStripeSubscriptions(db, connection, stripeConfig, features,
"community-2026-v3", time.Now)` in the shared Go server composition. It returns:

- `Store`: provisioning, receipt persistence, reconciliation and pending scans.
- `Management`: existing tenant resolution and `billing.manage` checks for checkout,
  portal and subscription reads; the caller supplies an authenticated principal.
- `Webhook`: mount at `POST /v1/billing/stripe/webhook` using the shared
  [OpenAPI specification](../../api/openapi/openapi.yaml).
- `Entitlements`: inject into the existing API, worker and premium-operation guards.
- `Synchronization`: webhook handling and `Retry(ctx, eventID)` for recovery.

Construct the feature/edition catalog from SafePatch's validated local
configuration (`config.V02Editions`); do not load feature mappings from Stripe.
Connection records identify provider, account reference and live/test environment.
The factory rejects a live/test mismatch and wires one directory and clock into
the adapter. Deployment must verify that the secret key and endpoint signing
secret belong to the configured account/environment. API and snapshot-event
version remain pinned to `2025-06-30.basil`.

Use a primary database with a trusted fixed search path, secure driver/TLS
configuration, and enough connections for concurrent requests. Reconciliation
holds a transaction while refreshing Stripe. In the supplied composition the
adapter's directory reads reuse that transaction, avoiding pool starvation even
with a single connection. Custom readers must honor the context and avoid blocking
on a second connection in the same pool. The transaction deadline is
30 seconds and the Stripe request timeout is 15 seconds; failures roll back.
Runtime entitlement reads have a ten-second deadline.

`RegisterConnection` and `BindCustomer` are trusted provisioning methods. Call
them only after verifying organization ownership and Stripe customer ownership;
they are not public endpoints and are never populated from webhook metadata.
Bindings are unique and immutable. The current milestone permits one customer
binding per organization. Changing accounts/customer ownership requires a reviewed
additive migration, not silently reassigning subscriptions.

## Durable delivery and reconciliation

The HTTP handler reads at most 256 KiB and authenticates the exact raw payload with
the existing HMAC verification, freshness, API-version and environment checks.
Only after verification does `SubscriptionNotification` resolve trusted customer
ownership and extract subscription routing identity. It does not apply historical
event price, status, or feature data.

Supported triggers are subscription created/updated/deleted/paused/resumed and
invoice paid/payment-failed/payment-action-required. Invoice routing uses the
Basil `parent.subscription_details.subscription` reference. Unsupported types,
including Stripe entitlement-summary events, are acknowledged without granting
access. Unknown current prices or failed synchronization are not acknowledged as
successful work.

`billing_events` persists a minimal receipt before processing. Its primary key is
`(connection_id,event_id)`, and its immutable identity includes the organization,
external subscription and event occurrence time. Reusing an event ID for another
identity is a conflict. Raw payment data, signatures, credentials and scanner/LLM
output are not stored in the inbox.

Processing starts a transaction and locks the organization row. After obtaining
the lock, it rechecks whether the receipt is already applied; duplicates make no
provider request and do not increment a subscription revision. For pending work,
it retrieves the current subscription from Stripe, including the latest invoice,
then checks organization/customer/price identity and local sealed configuration.
This prevents delayed payloads from restoring an earlier plan. Event timestamps
are diagnostic data, never ordering or deduplication keys.

The transaction inserts or revision-checks the existing organization-owned
`subscriptions` row, writes `billing_subscription_state`, appends immutable
`billing_subscription_history`, and completes the receipt. Creation/provider/
organization identity stays fixed. Terminal subscription identities cannot become
active again. Any error rolls back all these changes; the separately committed
receipt remains pending. An ambiguous commit can safely be retried because the
next attempt checks durable completion again.

The existing unique current-subscription constraint remains authoritative. A new
subscription conflicting with an older current subscription fails and remains
pending; reconcile the older subscription's cancellation before retrying. This
does not silently cancel another provider or manual contract. Checkout-attempt
orchestration must still prevent concurrent purchases upstream.

The handler returns 204 only after committed synchronization, durable duplicate
recognition, or an explicitly ignored event type. Other errors return 400/413/503
with stable text and no backend/credential details. Stripe can retry unsuccessful
deliveries. Recovery can page `Store.Pending(ctx, afterID, limit)` and call
`Synchronization.Retry` for each receipt; restart from an empty cursor on each
scan cycle. No in-memory verification handle needs to survive a restart. A
scheduled recovery loop/JetStream dispatcher is a composition responsibility;
this milestone does not start background services.

## Runtime access and plan changes

`RuntimeEntitlements` has no provider client and no cross-request cache. Each
decision loads a complete, organization-scoped repeatable-read PostgreSQL snapshot:
the organization state, Community fallback bundle, existing non-subscription
entitlement sources, and current synchronized subscription/bundle. It uses the
existing resolver's precedence and feature-limit semantics over those records.
Suspended/pending organizations, malformed data and database failures deny access.

Subscription grants come exclusively from SafePatch's sealed `edition_features`.
An active subscription also requires an expanded latest invoice in `paid` state
whose customer and subscription match. Unpaid active subscriptions produce no paid
grants. Trials follow SafePatch's explicit trial policy and expire at the earlier
trial/period boundary. Grants always have a finite synchronized expiry; missing
periods fail closed. Cancellation, payment delinquency and expiration remove the
subscription contribution while preserving any independently valid local sources.
Cancellation at period end retains paid access only through the paid boundary.

Historical canonical `SUBSCRIPTION` records are kept for audit but are not fed
back into the new runtime reader. They cannot restore a downgraded feature.
`organizationstore` remains the canonical historical repository; use the new
`Entitlements` service for synchronized SaaS runtime access, and do not wrap it
with an unversioned cache. The offline Community provider remains account-free.
Manual overrides, restrictions, promotions and Enterprise license records retain
their existing independent resolver semantics.

Derived grants use the internal subscription ID as `source_reference`. Neither
derivation nor plan changes edit/delete canonical grants, repositories, evidence,
memberships, edition history or usage/reservations. Per-feature limits come from
the local bundle. Separate resource quota policies retain the existing trusted
quota provisioning path; this change does not automatically publish the
`PROVISIONAL` resource tariffs or reset counters. Feature ownership never replaces
`PermissionService.Allowed`, quota admission, or production policy/approval.

## Validation

```powershell
go test ./...
go test -race ./...
go vet ./...
```

Focused tests cover receipt deduplication/collisions, transaction rollback and
commit failures, revision conflicts, namespace/tenant boundaries, current-state
refresh for delayed deliveries, invoice ownership, expiry, local bundle products,
permission denial after entitlement success, downgrade/cancellation freshness,
immutable history and bounded HTTP ingress. Local transports use fake credentials.
Native migration fixtures test fresh application, legacy-data preservation,
composite foreign keys, receipt immutability, rollback and plan changes:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresSubscriptionSyncMigration -count=1 -v
```

Native tests need `psql` and a disposable database configured through libpq. They
skip without configuration; scripted database/sql tests do not prove PostgreSQL
locking or SQL execution. No live Stripe product/payment, production listener,
real database migration, or background worker was started by this implementation.

Provider references: [subscription webhooks](https://docs.stripe.com/billing/subscriptions/webhooks),
[delivery ordering and duplicates](https://docs.stripe.com/webhooks#event-ordering),
and [subscription object fields](https://docs.stripe.com/api/subscriptions/object).
