# SafePatch marketing site

The current site includes `/` (landing), `/signin`, `/signup`, `/pricing`,
`/downloads`, and `/onboarding`. Sign-in and signup use the existing GitHub App
OAuth flow. Downloads list published platform archives and recheck organization
entitlements and permissions for private bundles. See the
[current release review](../docs/releases/2026-09-27-review.md) for validation
evidence and unresolved production blockers.

The site also serves `/onboarding`: GitHub signup/login, organization creation,
edition selection, checkout/trial, GitHub installation, repository/policy selection
and durable first-proof submission. Follow the [Go API and HTTPS setup](../docs/architecture/onboarding.md).
Developer/Team trial links default to the local onboarding route. The API owns
all entitlement decisions. Browser tests use mocked onboarding responses; a
configured worker is required for actual proof completion.

The Next.js/TypeScript pricing page is available at `/pricing`. It shows
exactly Community, Developer, Team, Business and Enterprise, with feature search,
an availability comparison, responsive cards, keyboard-accessible action dialogs,
and expandable questions. The sections below describe the original pricing milestone.

## Run locally

Use the Go security patch required by `go.mod`, Node.js 24, and npm. The app follows the official
[Next.js App Router installation](https://nextjs.org/docs/app/getting-started/installation).
Dependencies are pinned in `package-lock.json`.

```powershell
cd C:\Users\HP\Documents\safepatch\web
npm.cmd ci
npm.cmd run dev
```

Open `http://127.0.0.1:3000/pricing`. No database, API process, Stripe credentials,
external fonts or images are needed. If the Windows Go cache is inaccessible,
set `$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'` before starting.

## Product data and public destinations

`npm run dev` and `npm run build` first run `scripts/catalog.mjs`, which invokes
the fixed Go generator in `scripts/catalog/main.go`. It projects:

- The five permanent identities from `internal/domain/edition`.
- Enabled mappings and SDK/FUTURE availability from `internal/config/editions_v02.json`.
- Feature names, descriptions and categories from `internal/domain/feature`.
- Trial duration from the existing `internal/domain/trial` policy.
- Public copy and action configuration from `internal/config/pricing.go`.

The generated `.generated/product-catalog.json` is ignored and must never be
hand-edited. Next.js consumes it for both the page and `/v1/product-catalog`,
documented in the single root OpenAPI specification. Builds fail if generation
fails; they never silently reuse stale output. A running deployment is a build
snapshot: restart development or rebuild/redeploy to publish configuration changes.
Product release metadata is independent of edition identity and trial duration.

Optional public configuration must be set in the shell or CI **before** dev/build.
The generator does not load dotenv files. For example, using illustrative domains:

```powershell
$env:SAFEPATCH_DOWNLOAD_URL = 'https://downloads.example.com/safepatch'
$env:SAFEPATCH_TRIAL_URL = 'https://app.example.com/signup'
$env:SAFEPATCH_SALES_URL = 'mailto:sales@example.com'
$env:SAFEPATCH_BUSINESS_TRIAL_DAYS = '30'
npm.cmd run build
npm.cmd start
```

Replace the example URLs with published destinations. Only HTTPS URLs and a plain
sales mailto address are accepted; credentials and unsafe schemes are rejected.
Trial links preserve other query parameters and set the canonical `edition` key.
Point them to an authenticated organization-selection flow. They must not activate
a trial on GET. Configure Business duration consistently with the trial service;
it remains unavailable by default. Developer and Team use 14 days.

Without a published destination, the requested action remains a button and opens
an explanation. It never fabricates a download, signup success, sales submission,
or trial activation. Paid amounts default to unpublished; Enterprise uses contract
pricing. Set `SAFEPATCH_PUBLIC_PRICES` before the catalog build to a JSON object
keyed by `developer`, `team`, or `business`. Each entry requires `amount` (positive
decimal string), `currency` (uppercase three-letter code), and `interval` (`month`
or `year`). Use approved per-organization amounts matching your billing offers.
Display configuration never changes charges, entitlements, or permissions.
Community links to `/downloads` by default. Business can continue to plan setup
even when no trial is offered.

`/checkout` hosts Paddle Billing checkout. Follow the [Paddle setup guide](../docs/architecture/paddle-setup.md)
for the publishable client token, Go API secrets, approved domain, prices, webhook
destination and live acceptance. The payment script and iframe/network CSP
permissions are scoped to this page. Browser completion never grants access.

Catalog visibility grants no access. Organization runtime entitlements, independent
actor permissions, quota policies and verification readiness continue to be enforced
by the existing Go services. Planned features are explicitly labeled; SDK availability
means developer building blocks, not a deployed SaaS feature. Provisional resource
budgets are not advertised as published allowances.

## Validation

```powershell
npm.cmd run build
npm.cmd run typecheck
npm.cmd run test:e2e
```

Playwright starts a temporary production server on loopback port 3100. On Windows
it uses installed Microsoft Edge; elsewhere run `npx playwright install chromium`
first. Tests compare all cards and feature cells with the API, exercise actions,
keyboard focus, search, planned filtering, empty results, FAQ expansion, and layouts
at 1440, 768 and 390 pixels. Screenshots are saved to ignored `test-results/`.

From the repository root, run `go test ./...`, `go test -race ./...`, and
`go vet ./...`. Configuration tests prove catalog parity, exact actions, duration
parity, unsafe URL rejection, and immutable configuration. Existing premium
admission tests continue to prove entitlement and permission boundaries. No schema
change is needed; native PostgreSQL tests retain their existing opt-in requirement.

The site renders text through React escaping. Product presentation is generated
from Go; private account and download state comes from authenticated API routes.
Pages render dynamically with a fresh script nonce and a CSP that does not allow
arbitrary inline scripts. CSS retains inline-style support. The production edge
template in `deploy/web.nginx.conf` provides HTTPS, ingress limits, no caching,
and access logging without OAuth query strings.

## Milestone file inventory

Inspected before modification: `web/README.md`, the root README and architecture
tests, `internal/config/editions_v02.{go,json}`, the edition/feature catalogs,
`internal/domain/trial/trial.go`, application command/guard boundaries, HTTP
entitlement middleware, Stripe offer configuration, and `api/openapi/openapi.yaml`.

Created Go files: `internal/config/pricing.go`, `internal/config/pricing_test.go`,
and `web/scripts/catalog/main.go`.

Created web files (relative to `web/`): `.gitignore`, `.env.example`, `package.json`,
`package-lock.json`, `tsconfig.json`, `next-env.d.ts`, `next.config.ts`,
`playwright.config.ts`, `scripts/catalog.mjs`, `lib/catalog.ts`, `app/layout.tsx`,
`app/page.tsx`, `app/globals.css`, `app/pricing/page.tsx`, `app/pricing/pricing.tsx`,
`app/v1/product-catalog/route.ts`, and `tests/pricing.spec.ts`.

Modified existing files: root `README.md`, `web/README.md`,
`api/openapi/openapi.yaml`, `api/openapi/README.md`, and
`docs/architecture/repository-structure.md`. Generated catalog, build output and
browser screenshots are ignored artifacts. No migrations, edition assignments,
subscriptions, organization grants or quota records are changed.
