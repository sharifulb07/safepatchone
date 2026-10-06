# Paddle Billing setup for SafePatch

The integration is implemented in the working tree. **No Paddle account has been
configured, no real payment has been taken, and nothing has been deployed.** Start
with sandbox. Live readiness also depends on the unfinished product work in the
[release review](../releases/2026-09-27-review.md).

## What the code does

An authenticated organization billing manager selects a configured offer. Go
reserves a durable checkout attempt, creates a Paddle transaction with a fixed
price and quantity one, saves its ID, then returns a link to `/checkout`. Paddle.js
collects payment information; SafePatch never handles card details.

`POST /v1/billing/paddle/webhook` authenticates the exact request body with
HMAC-SHA256 and a five-second timestamp tolerance. Go checks that the subscription
actually belongs to the transaction created by this server. Browser custom data,
email, success callbacks, or `?paid=true` cannot establish ownership or access.
The shared PostgreSQL synchronizer deduplicates receipts, locks the organization,
fetches current subscription state, and derives finite entitlements from SafePatch's
sealed edition bundles. An active subscription also needs a completed transaction
covering its current price and billing period. Paused, canceled and past-due
subscriptions lose paid subscription grants. Independently valid trials/contracts
are still resolved by the existing entitlement service.

The website's **Manage billing** action opens the Paddle customer portal after
membership, `billing.manage`, Origin and CSRF checks. SafePatch discards the
provider's automatic sign-in token: GitHub authentication does not verify the
billing email typed at checkout. Paddle requires its email magic-link verification
before exposing customer-wide billing history and management. See [Paddle portal
authentication](https://developer.paddle.com/concepts/sell/customer-portal/).
Download access
uses the same entitlements on Windows, Linux and macOS; payment does not create
edition-specific executables or implement SDK/FUTURE capabilities.

## 1. Create the sandbox catalog manually

Open the [Paddle sandbox dashboard](https://sandbox-vendors.paddle.com/). Sandbox
and live accounts have separate credentials and data; see [Paddle's sandbox
guide](https://developer.paddle.com/sdks/sandbox/).

Create three products and one recurring price for each:

| SafePatch edition | Paddle product | Copy into `SAFEPATCH_PADDLE_PRICES` |
| --- | --- | --- |
| Developer | SafePatch Developer | `developer`: its `pri_...` ID |
| Team | SafePatch Team | `team`: its `pri_...` ID |
| Business | SafePatch Business | `business`: its `pri_...` ID |

You decide the real amounts, currency and billing interval. The implemented
checkout supports one recurring, per-organization price, quantity one. Choose
monthly or annual prices; the UI does not currently offer a cadence toggle.
Do not enable Paddle trial periods for these prices: SafePatch's separate
organization trial service handles trials. Community remains free; Enterprise
remains contract-managed. Do not create paid prices for these two editions.

## 2. Create credentials manually

In **Developer tools > Authentication**, create:

| Value | Where it goes | Required purpose |
| --- | --- | --- |
| API key, `pdl_sdbx_apikey_...` | Go API secret environment: `SAFEPATCH_PADDLE_API_KEY` | `transaction.read`, `transaction.write`, `subscription.read`, `customer_portal_session.write` |
| Client-side token, `test_...` | Next.js environment: `SAFEPATCH_PADDLE_CLIENT_TOKEN` | Initialize Paddle.js; this token is intentionally public |
| Paddle account/seller ID | Go API: `SAFEPATCH_PADDLE_ACCOUNT_ID` | Immutable local account/environment binding |

Use a current-format API key, set an expiry and plan rotation. The API key and
webhook secret must never go into a `NEXT_PUBLIC_` variable, browser code, source
control or chat. See [API authentication](https://developer.paddle.com/api-reference/about/authentication/)
and [client-side tokens](https://developer.paddle.com/paddle-js/about/client-side-tokens/).

## 3. Set the domain and payment link manually

Deploy the website and Go API behind the same HTTPS domain, for example
`https://app.example.com`. Replace the placeholders in
[`deploy/web.nginx.conf`](../../deploy/web.nginx.conf). The Go API and Next.js can
listen on loopback; only the HTTPS reverse proxy should be public.

In Paddle **Checkout > Website approval**, register the real domain. In
**Checkout > Checkout settings**, set the default payment link to:

```text
https://YOUR-DOMAIN/checkout
```

Use the same domain in `SAFEPATCH_PUBLIC_ORIGIN` and the existing GitHub OAuth
configuration. Publish your actual business/product details, support contact and
policies needed for Paddle's account/website approval; complete the requirements
shown in your account. Do not replace these with invented business details.
[Default payment link documentation](https://developer.paddle.com/build/transactions/default-payment-link/).

Paddle.js loads only on `/checkout` after the account check. Its iframe/network
permissions are restricted to that route; ordinary marketing/auth pages keep
their existing CSP. Returning to `/onboarding` never grants access.

## 4. Configure the webhook manually

In **Developer tools > Notifications**, create a URL destination:

```text
https://YOUR-DOMAIN/v1/billing/paddle/webhook
```

Choose API version **1**, real platform events (not simulation-only), and enable:

```text
subscription.created
subscription.activated
subscription.updated
subscription.canceled
subscription.paused
subscription.resumed
subscription.past_due
transaction.completed
transaction.payment_failed
transaction.past_due
```

Copy the destination's secret into `SAFEPATCH_PADDLE_WEBHOOK_SECRET`. Route this
endpoint directly to Go, preserving the body and `Paddle-Signature` header; the
Next.js presentation proxy intentionally does not forward billing webhooks. Keep
the server clock synchronized. See [notification setup](https://developer.paddle.com/webhooks/about/)
and [signature verification](https://developer.paddle.com/webhooks/about/signature-verification/).

## 5. Set SafePatch environment and database binding

Use [`deploy/paddle.env.example`](../../deploy/paddle.env.example) as the reference.
It is not automatically loaded. Configure variables in your service manager or
hosting platform. For PowerShell development, `$env:NAME = 'value'` applies to
processes launched from that shell; on Linux/macOS use `export NAME='value'`.
Use your secret manager for real secret values.

The Go API needs:

```dotenv
SAFEPATCH_BILLING_PROVIDER=paddle
SAFEPATCH_PADDLE_ENVIRONMENT=sandbox
SAFEPATCH_PADDLE_CONNECTION_ID=paddle_sandbox
SAFEPATCH_PADDLE_ACCOUNT_ID=YOUR_SELLER_ID
SAFEPATCH_PADDLE_API_KEY=YOUR_SANDBOX_API_KEY
SAFEPATCH_PADDLE_WEBHOOK_SECRET=YOUR_DESTINATION_SECRET
SAFEPATCH_PADDLE_PRICES={"developer":"pri_YOURS","team":"pri_YOURS","business":"pri_YOURS"}
SAFEPATCH_PUBLIC_ORIGIN=https://YOUR-DOMAIN
```

Retain the database, NATS, persistent session key and GitHub settings from the
[onboarding guide](onboarding.md). Remove `SAFEPATCH_STRIPE_API_KEY` from this
deployment; configuring both providers causes startup to fail rather than select
an account silently. Existing Stripe organizations require an explicit billing
migration; changing environment variables does not transfer their ownership.

Apply the ordered migration history once. A database already at migration 21
needs only `migrations/000022_paddle_checkout.up.sql`. Use your migration runner
or `psql -X --set=ON_ERROR_STOP=1 --file=migrations/000022_paddle_checkout.up.sql`
with secure libpq connection settings. Back up an existing database first.
The runtime account needs the new table but must not have schema-owner privileges.

Register the trusted connection once using your database administration channel;
replace the seller ID before running this statement:

```sql
INSERT INTO billing_connections
  (connection_id, provider, account_reference, live_mode)
VALUES ('paddle_sandbox', 'paddle', 'YOUR_SELLER_ID', false);
```

If it already exists, inspect it and confirm all four fields match. Do not update
or delete immutable billing history. Startup validates this exact binding. Customer
bindings are created automatically only after a verified server-created purchase.

Set these in the **Next.js process** separately:

```dotenv
SAFEPATCH_PADDLE_ENVIRONMENT=sandbox
SAFEPATCH_PADDLE_CLIENT_TOKEN=test_YOURS
SAFEPATCH_API_ORIGIN=http://127.0.0.1:8080
```

Set `SAFEPATCH_PUBLIC_PRICES` in the website **build environment** to your approved
amounts, currencies and intervals, then rebuild. It accepts a JSON object keyed
by `developer`, `team`, `business`; each value contains string `amount`, uppercase
`currency`, and `interval` (`month` or `year`). This changes display labels only;
Paddle's configured price controls the charge. Include all three plan amounts and
keep tax treatment clear to customers. Leave it unset until you approve prices.

Rebuild/restart both services after configuration. No extra Go or npm payment
dependency was added; the browser uses Paddle's hosted v2 script.

## 6. Run the real sandbox acceptance test

1. Sign in and create a test organization. Select Developer, Team or Business and
   use **Continue to checkout**. Confirm the expected price and currency appear.
2. Pay using Paddle's sandbox card `4242 4242 4242 4242`, any future expiry and
   security code `100`. Use a billing email you control. Test a decline using
   `4000 0000 0000 0002`. See [official test cards](https://developer.paddle.com/sdks/sandbox/).
3. Inspect the transaction and subscription in Paddle. Confirm notification
   delivery eventually returns `204`. A `503` before the first transaction reaches
   `completed` is retryable; it does not grant access early.
4. Return to onboarding, choose the same organization and click **Check payment
   or contract status** or **Refresh progress**. Verify its actual capabilities and
   download eligibility. S3 configuration/artifact publication is still required
   for real downloads; payment alone cannot fabricate binaries or signed links.
5. Use **Manage billing**, verify the billing email with Paddle, then view billing
   and cancel. Test immediate cancellation,
   scheduled cancellation, failed renewal, pause/resume, duplicate notification
   replay, and an older event delivered after cancellation. Scheduled cancellation
   keeps the paid period until its end; actual cancellation revokes paid grants.
6. Test an account without `billing.manage` and another organization. Neither may
   create checkouts or obtain another organization's portal/download links.

Webhook simulator events with fictional IDs alone cannot activate an organization:
the integration also reads real API entities and checks persisted transaction
ownership. Use an actual sandbox checkout for the complete acceptance test.

## 7. Move to live only after acceptance

Complete Paddle's live account/website approval and payout setup in the dashboard.
Create live products/prices, an API key, a client token and a notification
destination. Use a separate production database/account binding, for example
`paddle_live` with `live_mode=true`. Set `SAFEPATCH_PADDLE_ENVIRONMENT=production`
in both services, use `pdl_live_apikey_...`, `live_...`, the live webhook secret,
live price IDs and the approved live domain. Sandbox IDs never become live IDs.

Monitor failed webhook deliveries, unresolved checkout attempts, database failures
and API key expiry. Do not mark the whole SafePatch product production-ready solely
because a payment succeeds.

## Operational limits and recovery

- The existing billing schema binds one provider customer to one organization.
  Paddle uses unique customer emails, so separate organizations currently need
  distinct billing customer emails. Shared-customer/multiple-organization billing
  needs a schema and authorization redesign; never resolve it by linking emails.
- One unresolved checkout or active subscription blocks a new purchase for an
  organization. Reopening the same saved checkout is safe. Changing the selected
  plan does not upgrade an existing subscription: use the customer portal where
  available or an operator-managed Paddle subscription update to a configured
  price. Unmapped prices and Paddle-managed trials fail closed.
- A network/response failure during transaction creation leaves a durable
  reservation. This deliberately prevents a blind retry from creating duplicate
  purchases. Inspect `paddle_checkouts` and Paddle's transactions for matching
  `custom_data.safepatch_checkout`. After verifying the exact organization, offer,
  configured price, quantity and account, an administrator can fill a still-null
  `transaction_id` and its fixed-origin `checkout_url` from that existing Paddle
  transaction. Never delete the reservation or disable history guards. If no
  transaction can be established, keep the attempt blocked for operator recovery;
  automatic abandonment/reissue is not implemented.
- Current settlement lookup inspects the 30 newest completed subscription
  transactions. If no matching price and full billing period is found, paid
  grants are withheld. Unsupported mixed-item, seat, prorated-only, imported or
  manually invoiced subscriptions need additional implementation.
- Refunds/chargebacks do not automatically cancel a Paddle subscription in this
  integration. Apply your approved refund/access policy in Paddle, including
  cancellation when appropriate; automatic adjustment-based revocation is not
  implemented. Cancellation/past-due subscription events are handled.
- Failed notifications need monitoring and replay from Paddle. Already accepted
  events are durable and safe to retry. This change does not add a reconciliation
  scheduler or a provider-independent billing migration tool.

## Local validation

Executed in this checkout:

| Check | Result |
| --- | --- |
| Repository-wide Go tests | Passed |
| Billing/application/HTTP/host race tests and Go vet | Passed |
| Production website build and TypeScript check | Passed |
| Browser suite, including Paddle script/error fixtures | 14 passed |
| Real Paddle sandbox payment and webhook delivery | Not run: account credentials/configuration are still manual |
| Native PostgreSQL migration 22 | Not run locally: requires the configured disposable database and `psql` |

The Go tests cover signature tampering/replay, account/environment selection,
immutable ownership, configured pricing, unpaid periods, pause/cancel/past-due,
duplicate and out-of-order events, portal redirects and checkout retry behavior.
Browser tests exercise the checkout script boundary and failures with a Paddle
fixture. The native PostgreSQL migration test is opt-in and also runs in the
verification workflow; a local skipped integration is not live SQL evidence.

```powershell
go test ./... -count=1
go test -race ./internal/billing/... ./internal/platform/billingstore ./internal/platform/httpapi ./internal/platform/onboardinghost ./internal/application -count=1
go vet ./...
cd web
npm.cmd run build
npm.cmd run typecheck
npm.cmd run test:e2e
```

For the native migration test, configure a disposable PostgreSQL database through
the standard `PG*` variables, install `psql`, set
`SAFEPATCH_POSTGRES_MIGRATION_TEST=1`, and run
`go test ./internal/platform/migrationtest -run TestPostgresPaddleCheckoutMigration -count=1 -v`.
The test creates and removes its own schema.
