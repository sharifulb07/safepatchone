# Organization onboarding

`/onboarding` guides GitHub signup/login, organization creation, edition selection,
checkout or organization trial, GitHub App installation, repository selection,
policy selection and durable first-proof submission. The Go API owns every state
transition. The Next.js page renders the shared product catalog and server state.

Selecting an edition only saves a requested sealed bundle and advances the
onboarding revision. Checkout creation, browser redirects, query parameters and
GitHub installation callbacks cannot grant capabilities. The access gate reads
the committed SafePatch subscription observation or the existing organization
trial record, including expiration. Actual operations independently call the
shared entitlement service and permission service. Stripe remains a billing
adapter, never the runtime entitlement database. Community uses the configured
default bundle. Changing the selection neither cancels a paid subscription nor
deletes repositories, grants, usage or evidence.

Developer and Team trials use the existing 14-day policy. Business duration must
be configured consistently in the API and marketing build. Enterprise checkout
is not offered; a trusted contract adapter must synchronize an authoritative
subscription observation before this onboarding gate recognizes the contract.
Standalone license grants are not currently an onboarding billing-completion
source. No client action provisions a contract.

## Deployment

Apply the one ordered migration history through `000011_onboarding.up.sql` once,
using the existing [migration procedure](../../migrations/README.md). There is no
automatic migration runner. Provide PostgreSQL, NATS JetStream and an HTTPS
reverse proxy. The API listens on loopback by default. Serve the Next.js site and
API under one public HTTPS origin; Secure `__Host-` cookies require HTTPS.

Configure these server-only values through your secret manager or environment:

| Variable | Purpose |
| --- | --- |
| `SAFEPATCH_PUBLIC_ORIGIN` | Public HTTPS origin, without trailing slash or path |
| `SAFEPATCH_DATABASE_URL` | PostgreSQL connection configured for trusted TLS/authentication |
| `SAFEPATCH_NATS_URL` | Authenticated broker connection; use TLS outside loopback |
| `SAFEPATCH_PROOF_STREAM` | Provisioned stream containing `safepatch.tenant.*.proof`; see [tenant-boundary rollout](tenant-boundaries.md) |
| `SAFEPATCH_SESSION_KEY` | Base64 encoding of 32 cryptographically random bytes; keep stable across replicas |
| `SAFEPATCH_GITHUB_CLIENT_ID` | GitHub App OAuth client ID |
| `SAFEPATCH_GITHUB_CLIENT_SECRET` | GitHub App OAuth client secret |
| `SAFEPATCH_GITHUB_APP_SLUG` | Installed GitHub App slug |
| `SAFEPATCH_BUSINESS_TRIAL_DAYS` | 0 disables the offer; otherwise the existing trial-policy range |
| `SAFEPATCH_API_ADDR` | Optional listen address, default `127.0.0.1:8080` |

Register the GitHub App callback as
`<public-origin>/v1/auth/github/callback`. Request only the read access needed
for verification (repository metadata and contents); repository code is never
executed in the API process. Signup and login share the App's user authorization
flow. GitHub subject IDs identify users; matching an email never links accounts.
The flow uses [GitHub's user-token authorization](https://docs.github.com/en/enterprise-cloud%40latest/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app)
with state and S256 PKCE. Repository lists use the authenticated user's accessible
installations, rather than trusting a browser-supplied owner or clone URL.

For checkout, also set `SAFEPATCH_STRIPE_API_KEY`,
`SAFEPATCH_STRIPE_WEBHOOK_SECRET`, `SAFEPATCH_STRIPE_CONNECTION_ID`,
`SAFEPATCH_STRIPE_ACCOUNT_ID`, and `SAFEPATCH_STRIPE_PRICES`. The last is a JSON
object mapping `developer`, `team`, and `business` to configured recurring price
IDs. Register the provider connection using the existing trusted billing
provisioner before startup. The API creates and binds organization customers
idempotently; it cannot accept a customer ID from the browser. Route Stripe
deliveries directly to `/v1/billing/stripe/webhook`, preserving the raw body.
See [subscription synchronization](stripe-subscriptions.md).

Start the Go API from the repository root:

```powershell
go run ./cmd/safepatch-api serve
```

For Next.js, set server-only `SAFEPATCH_API_ORIGIN=http://127.0.0.1:8080` when the
API is on the same host, or use its trusted HTTPS origin. Start the site from
`web` with `npm.cmd run build` then `npm.cmd start`. Access `/onboarding` through
the public HTTPS proxy. The bounded Next.js proxy forwards only auth/onboarding
paths and selected headers to this fixed origin; it never chooses a destination
from user input. No `NEXT_PUBLIC_` secret is needed. Pricing trial links now
default to `/onboarding?edition=...` when no external trial URL is configured.

## First-proof worker boundary

Submission commits the job and saved proof ID in one PostgreSQL transaction.
An outbox loop retries publication. Only a durable JetStream acknowledgement
marks publication; the stable message ID deduplicates retries. Queue messages
contain organization and proof IDs, never credentials, actor claims, commands,
source paths or results. Workers reload the stored actor and request.

`onboardingstore.Proofs.Consume` binds a pre-provisioned durable pull consumer.
Configure explicit acknowledgements, AckWait greater than the six-minute lease,
bounded pending messages/deliveries and appropriate subject ACLs. Compose it with
`NewGuardedExecutor` using the current organization runtime, permission service,
atomic quota admission and GitHub adapter. Every selected repository is checked
again before the executor runs. Missing quota policy denies admission.

The executor port must wrap the existing verifier SDK, container sandbox and
immutable evidence store, pin source revisions, apply the configured policy and
persist evidence before reporting its reference. This milestone provides the
outbox, publisher, consumer and fenced result persistence, **not a hosted source
fetcher or a concrete deployed verifier/evidence executor**. The API command does
not start a verification worker. Without that composition, submissions stay
pending/queued and the page explicitly reports that the proof has not completed.
It must never simulate PASS. Timeouts, execution errors, invalid statuses and
PASS/FAIL without evidence become UNKNOWN. Completed results cannot be rewritten
by delayed workers. Even PASS is not production deployment authorization.

## Security and validation

Opaque sessions are stored hashed. GitHub access tokens and PKCE verifiers are
encrypted using AES-GCM with record-bound associated data. Sessions expire within
eight hours or token expiry; token refresh and additional identity providers are
not implemented. Login attempts expire after ten minutes and are atomically
consumed once. POST requires the exact configured Origin and session-bound CSRF
token. Provider redirects are disabled, API destinations are fixed, bodies and
pagination are bounded, and unknown/duplicate JSON fields are rejected. Provider
errors and credentials are never returned to the browser.

Organization creation is idempotent per user/attempt and currently limited to
20 organizations created per user. One GitHub installation binds to one
organization. Member permissions remain organization-specific. Migrated legacy
organizations need an explicit onboarding-state bootstrap before using this flow.
Session cleanup and the ingress limiter bound normal traffic; production ingress
must also enforce abuse controls. The in-process limiter keys actual socket IP,
not spoofable forwarded headers; a proxy therefore shares its local quota.

Run `go test ./...`, `go test -race ./...`, and `go vet ./...`. Unit and scripted
transaction tests cover authoritative access, concurrent edits, tenant/permission
boundaries, private repositories, policy gating, downgrade/revocation, provider
errors, CSRF, encrypted credentials and unavailable brokers. The new native
migration fixture checks retained commercial data, composite tenant references,
revision fencing and immutable proof history. Enable it with the existing
`SAFEPATCH_POSTGRES_MIGRATION_TEST=1` setup; it otherwise reports a skip.

In `web`, run `npm.cmd run build`, `npm.cmd run typecheck`, and
`npm.cmd run test:e2e`. Browser onboarding tests mock the API to exercise the full
presentation flow, forged success parameters, backend failure and UNKNOWN
results. They do not establish live GitHub, Stripe, PostgreSQL, NATS, sandbox or
S3 interoperability. Complete a sandbox deployment smoke test before release.

Validation in this workspace on 2026-09-07: `go test ./...`,
`go test -race ./...`, `go vet ./...`, the Next.js production build, TypeScript
check and all seven browser tests passed. The onboarding migration contract test
passed; native PostgreSQL execution was skipped because the database/libpq test
environment is not configured. The mobile onboarding screenshot was inspected.
No real provider credentials or hosted proof worker were exercised.

## Milestone inventory

Inspected: organization, edition and feature domains; existing billing and trial
services/stores; authorization, tenant, quota and verification boundaries;
API command and specification; migrations and native test harness; pricing
configuration, Next.js routes, security configuration and browser tests.

Created: `internal/domain/onboarding/`, `internal/application/onboarding*.go`,
`internal/config/onboarding.go`, `internal/platform/onboardinghttp/`,
`internal/platform/onboardingstore/`, `internal/platform/onboardinghost/`,
`internal/platform/billingstore/onboarding*.go`,
`internal/billing/stripe/customer*.go`, migration 000011 and its test/fixtures,
`web/app/onboarding/`, `web/app/v1/[...path]/route.ts`,
`web/tests/onboarding.spec.ts`, and this document.

Modified: API command/composition help, Go dependencies, pricing default trial
destination/tests, single OpenAPI specification and README, migration README,
root README, web README and example environment. No permanent edition,
existing migration, entitlement key or verification engine is replaced.
