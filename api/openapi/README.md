# Shared API specification

The GitHub App webhook is `/v1/integrations/github/webhook`. It verifies the exact
body, resolves the organization installation/repository binding, checks current
entitlements and actor permission, and commits durable PR work. See
[GitHub App setup and worker contract](../../docs/architecture/github-app.md).

The same specification now includes GitHub signup/login and organization
onboarding. `onboardinghttp` implements the server boundary, mounted by
`safepatch-api serve`. See [setup, authority and worker limits](../../docs/architecture/onboarding.md).
The Next.js proxy exposes auth/onboarding under the same HTTPS origin. All
mutations require a session, exact Origin and CSRF token.

The single SafePatch API specification is [openapi.yaml](openapi.yaml). It defines the public `/v1/product-catalog` read and shared Stripe subscription webhook contract. The Next.js marketing site serves the public Go-generated catalog; it contains no tenant data and grants no access. `httpapi.NewBillingWebhookHandler` implements bounded, verified webhook ingress; production composition must mount it explicitly. Do not create edition-specific specifications. User operations must enforce tenant scope, entitlements, and actor permissions on the server; webhook system authority comes from signature verification and trusted ownership bindings.

Reusable HTTP enforcement now lives in `internal/platform/httpapi`. Its [middleware contract](../../docs/architecture/feature-enforcement.md#api-middleware) documents mandatory authentication/scope/permission/quota dependencies and structured error responses. Future routes must install it explicitly; no endpoint or separate edition specification is introduced by the helper.
