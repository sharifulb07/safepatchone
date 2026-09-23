# Provider-neutral billing

The follow-on [Stripe subscription integration](stripe-subscriptions.md) now
implements durable receipts, customer bindings, reconciliation and local runtime
entitlement derivation. This page documents the lower-level provider port; its
normalization methods still do not write subscriptions or issue grants directly.

SafePatch has one `BillingProvider` contract in `internal/domain/billing` and one
organization-owned subscription model. `internal/billing/stripe` implements
`StripeBillingAdapter`; no Stripe SDK, HTTP type, price ID, or webhook signature
format is imported by core domain/application code. The adapter uses the standard
library and adds no dependency or database migration.

| Port operation | Result |
| --- | --- |
| `CreateCheckout` | Hosted subscription checkout session for an organization and configured offer |
| `ReadSubscription` | Provider-neutral subscription observation scoped to an organization |
| `CreateCustomerPortal` | Hosted management session for that organization's customer |
| `ProcessVerifiedWebhookEvent` | Normalized subscription event from an adapter-issued verified handle |

`WebhookVerifier.VerifyWebhook` is a separate ingress port. An interface value
whose `Provider()` says `stripe` is not proof of verification: Stripe processing
accepts only its private handle issued by the same adapter instance, while its
signature timestamp remains valid. The handle cannot be serialized into a queue
and later treated as verified authority.

## Organization ownership and authorization

`application.BillingService` takes an authenticated principal, resolves the
organization scope using the existing tenant resolver, and calls the existing
`PermissionService.Allowed(..., billing.manage)` before checkout, portal, or
subscription reads. Authentication itself remains the composition caller's
responsibility. Provider and permission failures expose stable stage errors.

Billing management is not a premium feature: an organization may purchase a
capability it does not yet own. No feature key is introduced for checkout. Actual
premium operations still require both existing entitlement and permission checks.
An LLM response, checkout redirect, verified event, or active billing status does
not issue an entitlement or authorize production.

Every adapter call uses `organization_id`; there is no paid user/repository
subscription. A trusted `CustomerDirectory` supplies both directions of the
organization/customer binding. Bindings must be unique and immutable within the
adapter's Stripe account and test/live environment. Calls cross-check both
directions. Subscription metadata is compared with this authoritative binding;
metadata alone cannot select an organization. Missing or inconsistent bindings
fail closed before any checkout or portal session is created.

The directory is an injected persistence port. Customer creation, storage of its
bindings, and provisioning are not implemented here. Use an existing, verified
Stripe customer for checkout. Do not populate the directory from browser fields
or webhook metadata, and do not reuse it across Stripe accounts/environments.

## Stripe configuration and transport

Construct `stripe.NewStripeBillingAdapter(stripe.Config{...})` in trusted
composition with:

- A secret/restricted API key and matching `LiveMode`, loaded from secret storage.
- One to three endpoint signing secrets to permit bounded key rotation.
- An immutable edition catalog and an explicit list of
  `Offer{OfferID, EditionVersionID, PriceID}` mappings. The catalog must contain
  each pinned bundle. Duplicate offers/prices and unknown bundles are rejected.
- The account/environment-scoped `CustomerDirectory`.
- Fixed HTTPS success, cancellation, and portal-return URLs controlled by SafePatch.

Requests contain an opaque offer ID, never a client-selected price, amount,
feature grant, software version, or release channel. All offers use the same
checkout code. This milestone supports a single recurring base price with quantity
one. Keep old price mappings available while historical subscriptions use them.
Changing a price through the portal changes the observed bundle through trusted
price configuration, not through mutable edition-name branching or metadata.

REST requests target only `https://api.stripe.com/v1/` and pin
`Stripe-Version: 2025-06-30.basil`. Configure the webhook endpoint to emit snapshot
events at that same version. A different event version or Connect account is
rejected. Upgrading the pinned version requires updating and validating fixtures.

The HTTP client has a 15-second timeout, bounded connection settings and 256 KiB
response limit. Its default transport does not use environment proxies. Redirects
are rejected; provider error bodies and transport error strings are not exposed.
The injectable transport is trusted infrastructure/test configuration. Customer
redirect results must use HTTPS on `checkout.stripe.com` or `billing.stripe.com`;
custom Stripe checkout domains are not supported in this milestone. Session URLs
contain sensitive tokens: return them only to the authorized caller, never log them.

Checkout and portal POSTs require a caller-provided idempotency key. The adapter
hashes operation, organization and attempt identity into the Stripe key. Retries
of one attempt reuse it; changed parameters need a new attempt. This is Stripe
request idempotency, not a durable SafePatch checkout-attempt ledger. Trusted
checkout orchestration must prevent simultaneous attempts for an organization
that already has a current main subscription.

## Webhook verification and processing

Ingress must cap the HTTP body before reading it, then pass the exact original
bytes and `Stripe-Signature` header to `VerifyWebhook`. The adapter independently
bounds input size, checks HMAC-SHA256 in constant time, supports multiple `v1`
signatures/rotating secrets, and rejects timestamps more than five minutes old or
in the future. Verification precedes JSON parsing. Duplicate JSON keys, invalid
UTF-8, excessive nesting, malformed identities, wrong environment and API-version
mismatches fail closed. Keep the host clock synchronized.

Processing supports `customer.subscription.created`, `customer.subscription.updated`
and `customer.subscription.deleted`. Other verified event types return
`ErrUnsupported`, without side effects. Subscription objects require an owned
customer, matching organization metadata and one mapped recurring price. Unknown
states/prices and incomplete item pages are rejected. Paused/unpaid/past-due billing
normalizes conservatively to `PAST_DUE`; it never becomes active access. Collection
pauses also prevent an `ACTIVE` observation. Cancellation at period end remains
visible as a separate flag until the provider reports cancellation.

`ProcessVerifiedWebhookEvent` returns an immutable-by-value `SubscriptionEvent`
with provider event ID, occurrence time, and a subscription snapshot. It does not
write the database, issue grants, publish work or acknowledge HTTP delivery.
Repeating it has no persistence side effects. Returned snapshots do not contain a
database revision or an internal subscription ID.

The follow-on subscription integration supplies a durable inbox that deduplicates
within a provider/account/environment connection, serializes reconciliation per
organization, reloads current provider state, and atomically updates the existing
organization-owned subscription using CAS. Distinct Stripe events can have the
same timestamp; timestamps are not ordering or deduplication keys. Runtime access
reads the synchronized local state without a cross-request cache, so no external
cache invalidation is required. The handler acknowledges applied work only after
commit. See the integration documentation for transaction behavior and deployment
requirements, including pending-receipt recovery.

## Additional providers

GitHub Marketplace, AWS Marketplace and manual enterprise contract adapters can
implement the same four-method contract without changing domain subscription,
entitlement, edition, or usage schemas. External references and offer IDs are
opaque bounded identifiers. Each adapter owns its verification mechanism and
private verified handle; an unavailable checkout or portal can explicitly return
`ErrUnsupported`. No fake success or speculative marketplace implementation is
included. Manual contracts require trusted issuance, not a public self-asserted
payment event.

## Validation and scope

Run:

```powershell
go test ./internal/domain/billing ./internal/billing/stripe ./internal/application ./internal/platform/architecture -count=1
go test ./...
go test -race ./...
go vet ./...
```

Tests use fake credentials and a local TLS server/transport. They exercise checkout
forms, customer binding, idempotency, portal/read flows, signatures, forgery,
replays, key rotation, ownership errors, response limits, redirects, failure
redaction, permission denial, concurrent normalization and preserved bundle history.
Architecture fixtures forbid provider imports into core and billing inputs into
authorization/verification semantics. Existing entitlement, quota, plan-change,
and migration suites remain shared across all editions.

No live Stripe account/payment, production API endpoint, PostgreSQL customer
directory/inbox, checkout orchestration, billing reconciliation, tax/refund flow,
marketplace integration, or entitlement issuance is claimed by these tests.

Implementation references: [Checkout creation](https://docs.stripe.com/api/checkout/sessions/create),
[subscription retrieval](https://docs.stripe.com/api/subscriptions/retrieve),
[portal creation](https://docs.stripe.com/api/customer_portal/sessions/create),
[signature verification](https://docs.stripe.com/webhooks/signature),
[event ordering and duplicate deliveries](https://docs.stripe.com/webhooks#event-ordering),
and [request idempotency](https://docs.stripe.com/api/idempotent_requests).
