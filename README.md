# SafePatch

See the [2026-10-03 review](docs/releases/2026-10-03-review.md) for the latest
security upgrade, authentication and shutdown fixes, validation, and release gates.
On Windows, run `powershell -NoProfile -ExecutionPolicy Bypass -File tools/verify.ps1` for reproducible local checks.
Use `-Postgres` with a disposable database to also run live migration acceptance.

See the [2026-09-27 review and deployment guide](docs/releases/2026-09-27-review.md)
for the landing/account/pricing/download flows, security fixes, validation, and
remaining production release blockers. Use the Go security patch in `go.mod`.

The [standard release pipeline](docs/releases.md) builds all six shared Go commands
for Linux, macOS and Windows, plus signed container images, SBOMs, checksums and
provenance. One artifact set serves all five editions through runtime entitlements.

The [GitHub App PR integration](docs/architecture/github-app.md) adds verified
webhooks, tenant-bound durable jobs, shared local/hosted change verification and
GitHub Checks. The setup guide identifies required worker/source/storage adapters.

Organization onboarding is available at `/onboarding`. See the
[onboarding setup and worker boundary](docs/architecture/onboarding.md) for
GitHub login, the Go API, authoritative billing/trials and first-proof submission.

SafePatch is a software-change verification and trust platform being developed in Go. This checkout contains shared core models, entitlement and quota infrastructure, an organization account API, command shells, verification runner adapters, tests, and design documents. End-to-end verification and production worker composition are **not implemented yet**.

The [v0.1 release preparation](docs/releases/v0.1.md) records the shared developer-preview scope and release validation. The [v0.1 edition capability matrix](docs/editions/v0.1-feature-matrix.md) covers all five editions, distinguishes implemented SDK code from product workflows, and marks later capabilities **FUTURE**.

The shared [FeatureCatalog](docs/architecture/feature-catalog.md) now defines and validates 39 stable capability identities. These are planned metadata entries, not organization grants or implemented premium workflows.

The [Edition model](docs/architecture/edition-model.md) defines five permanent offerings and immutable versioned feature mappings, backed by the first shared PostgreSQL migration. No edition name directly grants access.

[Initial edition bundles](docs/architecture/initial-edition-bundles.md) are published by the second additive migration, entirely as seed data. They contain 4, 9, 16, 23, and 32 enabled mappings respectively; no customers are assigned by the seed.

The [canonical Entitlement model](docs/architecture/entitlement-model.md) and organization-scoped `EntitlementRepository` interface define provider-independent grant records. The [EntitlementResolver](docs/architecture/entitlement-resolver.md) provides deterministic precedence, security restrictions, and Has/Limit/Resolve ownership checks. Canonical grant persistence uses the shared organization store; authoritative multi-source ingestion remains to be wired.

[Effective entitlement snapshots](docs/architecture/effective-entitlement-snapshot.md) reuse that resolver behind a bounded, short-lived cache. Every use checks source revisions; scheduled changes and expiry force refresh, with no stale fallback. The consistent snapshot-source persistence adapter is still required before production use.

[PermissionService](docs/architecture/permission-service.md) independently checks owner/viewer memberships and eleven permission keys. Shared guarded operations enforce authenticate → tenant resolve → entitlement → permission → quota → operation. Shared PostgreSQL membership and tenant adapters are available; authentication and live operation composition remain unimplemented.

Community, Developer, Team, Business, and Enterprise are five editions of this one platform. **Software Version != Commercial Edition != Release Channel.** Runtime feature decisions must use entitlements and actor permissions, never edition-specific application forks.

[Entitlement limits and quotas](docs/architecture/entitlement-limits-quotas.md) provide eight typed resource policies and atomic Check/Reserve/Commit/Release accounting, with PostgreSQL and bounded memory adapters. The third additive migration preserves existing editions and usage across policy changes. Production driver/composition, policy ingestion and abandoned-work reconciliation remain to be wired.

[CommunityEntitlementProvider](docs/architecture/community-entitlement-provider.md) supplies the built-in `community-2026-v2` bundle without an account, subscription, Stripe, or network. Inspect it with `safepatch entitlement show` or `safepatch entitlement show --json` (`go run ./cmd/safepatch entitlement show` from source). Local evidence joins the preserved Community grants; cloud-only capabilities remain denied. Entitlement and implementation status are displayed separately; CLI verification composition remains planned.

The [entitlement-aware CLI](docs/architecture/cli-entitlements.md) adds `safepatch entitlement explain <feature>`, `safepatch edition`, and `safepatch whoami`. All accept `--json`. Disabled-feature requests return `FEATURE_NOT_ENTITLED` on stderr and exit 1; JSON errors expose `error.code`. `whoami` reports an unauthenticated offline session. CI receives no sales messages.

[Reusable feature enforcement](docs/architecture/feature-enforcement.md) shares ownership checks across `RequireEntitlement` API middleware, the CLI `RequireFeature` helper, and worker dispatch. API requests independently enforce authentication, tenant scope, entitlements, permissions and quota; workers reload trusted work records and recheck access before each start. Production endpoints, consumers and persistence composition remain unimplemented.

The shared [verification runners](docs/architecture/verification-runner.md) add `Runner`, `ExecutionRequest`, `ExecutionResult`, trusted `LocalRunner`, and `ContainerSandboxRunner`. Container execution requires an immutable local image and enforces identity, network, filesystem, resource and output controls. The runner infrastructure does not implement scanners or turn process completion into a verification PASS.

The [capability-based Verifier SDK](docs/architecture/verifier-sdk.md) defines verifier identity/version, capability discovery, Detect/Plan/Execute/Normalize, and versioned normalized evidence. It reuses the shared Runner and application access guards; entitlement policy controls admission without changing verifier internals. No concrete scanner or premium workflow is enabled by the SDK.

The [Go verification adapter](docs/architecture/go-verification.md) implements build, test, race, vet, Staticcheck, Govulncheck, fuzz and benchmarks through the shared SDK and secure Runner. It supports go.mod, go.work and nested modules. Security scanners use the existing `verify.security` capability at the application boundary; verifier behavior remains edition-independent.

[External scanner adapters](docs/architecture/scanner-integrations.md) integrate configured Gitleaks, Semgrep, Trivy, OSV-Scanner and Syft engines through the same SDK and Runner. They normalize reports and preserve exact original output in scoped immutable storage. Community can use installed tools under its existing security entitlement; hosted execution requires authoritative tenant access and quota admission. Runtime command/server composition remains unimplemented.

The shared [immutable Evidence model](docs/architecture/immutable-evidence.md) seals tenant/change/run identity, producer metadata, status, payload, source locations, artifact references and input hashes into one edition-independent schema. It provides strict decoding, defensive copies, canonical hashing and conversion from existing verifier records.

The shared [EvidenceGraph](docs/architecture/evidence-graph.md) persists immutable Evidence and six directed relationship types in PostgreSQL. Tenant-scoped queries and application admission enforce repository ownership, feature grants, actor permissions and retention without changing graph semantics between editions.

The [revision-aware Causal ChangeGraph](docs/architecture/causal-change-graph.md) extends the same PostgreSQL adapter with immutable revision-pinned entities and evidence-backed causal assertions. Files, symbols, APIs, DB, events, services, tests, dependencies, runtime observations and incidents use one graph model. The guarded service rechecks graph scope capabilities, actor permissions and retention for every operation.

The shared [policy engine](docs/architecture/policy-engine.md) evaluates configured evidence, risk and proof requirements with repository/criticality selectors. It provides deterministic explanations and fails closed on missing facts. Central policy validation separately requires `policy.central` and `policy.manage`; commercial changes do not change evaluation semantics.

The [v0.2 edition matrix](docs/editions/v0.2-feature-matrix.md) publishes five additive bundle revisions and provisional resource-limit templates. It separates ownership from SDK/FUTURE availability and preserves existing pinned assignments, quota accounting and evidence.

The canonical [IntentContract](docs/architecture/intent-contract.md) gives every edition the same twelve-field change-intent schema, immutable canonical serialization and bounded validation. Existing intent capabilities gate operations through shared application guards without removing contract fields.

Shared [SpecDoctor and SpecForge](docs/architecture/specdoctor-specforge.md) validate explicit contracts, provide advisory ambiguity scores and suggestions, and generate immutable declarative evidence checks using the existing policy engine. Fixed `intent.basic`, `intent.advanced` and `intent.specforge` admission guards keep advanced work separate from offline Community validation. Unbound assertions remain UNKNOWN; no generated result authorizes production.

[IntentGuard](docs/architecture/intent-guard.md) combines the same contract, explicit semantic component facts, a bounded Git diff and evaluated SpecForge properties. It produces deterministic compliance, scope-drift and protected-property assessments with immutable Evidence IDs. Basic evaluation uses `intent.basic` plus independent authorization; incomplete inputs and missing checks cannot produce PASS.

The shared [Semantic Intermediate Representation](docs/architecture/semantic-intermediate-representation.md) normalizes language-adapter concepts into thirteen code/operation kinds. Immutable, versioned snapshots preserve source hashes, locations, references and extraction coverage with no edition concepts. Unmapped concepts fail explicitly; SIR validity is not verification PASS.

The [LanguagePack SDK](docs/architecture/language-pack-sdk.md) registers Go, JavaScript, TypeScript, Python and Java in one source tree. Each pack detects language metadata and normalizes supplied SIR concepts; Go also reuses its existing verifier. Advanced normalization requires the existing `language.additional_packs` group plus actor permission, while offline Community retains metadata detection.

The shared [ProofPlanner](docs/architecture/proof-planner.md) selects cumulative L0–L8 work using configured technical capabilities, provider readiness and remaining budgets. The application boundary rechecks `proof.planner`, provider grants and actor permissions. Missing advanced providers leave explicit gaps; a READY plan is proposed work, not achieved proof or production authorization.

The [organization-first SaaS entities](docs/architecture/organization-entities.md) add users, multi-organization memberships, projects, repositories and organization-owned subscriptions. PostgreSQL persists the existing Entitlement schema and reuses the quota usage ledger; composite foreign keys prevent cross-organization ownership. Legacy tenant identities are preserved as inactive placeholders.

The [BillingProvider contract and Stripe adapter](docs/architecture/billing-provider.md) support organization checkout, subscription reads, customer portal sessions and verified webhook normalization. The application boundary enforces `billing.manage`; the core has no Stripe dependency.

[Paddle Billing setup](docs/architecture/paddle-setup.md) adds server-created checkout,
verified subscription synchronization, and customer billing management to the same
entitlement system. Follow the guide for manual dashboard settings and credentials.

[Stripe subscription synchronization](docs/architecture/stripe-subscriptions.md) adds Developer, Team and Business offer configuration, durable idempotent webhook receipts, transactional subscription/audit updates, and a shared HTTP handler. Runtime entitlements derive from PostgreSQL state and SafePatch's sealed edition bundles, with finite paid/trial boundaries and separate actor permissions. Enterprise remains contract-managed. Provisioning credentials/customers, live PostgreSQL/Stripe validation, mounting the handler and scheduling recovery remain deployment responsibilities.

[Organization trials](docs/architecture/organization-trials.md) offer Developer and Team for 14 days and Business for a configured duration. Atomic organization-owned trial records publish finite grants through the existing entitlement system. Expiration automatically falls back to Community and other applicable paid sources, without deleting data or requiring a scheduler. Trial management requires organization membership and `billing.manage`.

The [pricing page](web/README.md) presents the five permanent editions from a Go-generated product catalog, with the requested download, trial and sales actions, responsive comparison, and explicit SDK/planned availability. Public prices and destinations remain configuration inputs; the page does not activate trials or grant capabilities.

## Development

Use Go 1.26 or later. The Go adapter uses the pinned official `golang.org/x/mod` parser. The local module path is `safepatch`; no remote repository identity has been assumed. On Windows, race tests require CGO and a compatible C compiler.

```powershell
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/...
go run ./cmd/safepatch --help
```

All six commands support `--help`, `-h`, and `help`. Running `safepatch` without arguments (or `go run main.go` from `cmd/safepatch`) displays help and exits 0. `safepatch entitlement show` prints the offline Community bundle and exits 0. The other command shells report that the operation is not implemented when started without arguments and exit 1. `safepatch verify` also exits 1; it cannot report a successful verification. Other unsupported arguments exit 2. `safepatch-api serve` starts the account API with trusted PostgreSQL, NATS, GitHub and optional billing/download configuration; see the deployment guides above.

## Layout and boundaries

See [repository structure](docs/architecture/repository-structure.md) for the requested directory map, dependency policy, and test details. [One-codebase editions](docs/architecture/one-codebase-editions.md), [threat model](docs/security/threat-model.md), and [version/edition matrix](docs/roadmap/version-edition-matrix.md) describe the product requirements and remaining work.

There is one root Go module. `cmd/` composes the shared `internal/application` package, which depends inward on `internal/domain`. Other internal packages declare their intended boundaries without speculative feature implementations. Language/framework/provider integrations live outside core domain code. There are no edition directories. The Next.js site serves public pages and account/download interfaces backed by the Go account API.

This checkout has its own Git root. Keep Git operations scoped to SafePatch and preserve existing working-tree changes during reviews.
