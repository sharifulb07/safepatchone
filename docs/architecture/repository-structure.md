# Shared repository structure

Status: scaffold implemented on 2026-09-05. The earlier [current-state audit](current-state.md) records the empty starting workspace. There was no existing Go implementation to move or rewrite.

## Directory map

```text
cmd/
  safepatch/
  safepatch-api/
  safepatch-worker/
  safepatch-collector/
  safepatch-mcp-gateway/
  safepatch-replay/
internal/
  domain/
  application/
  entitlement/
  authorization/
  licensing/
  billing/
  tenant/
  config/
  verification/
  evidence/
  intent/
  spec/
  sir/
  graph/
  proof/
  policy/
  risk/
  trust/
  provenance/
  agent/
  replay/
  runtime/
  audit/
  platform/
    architecture/
languages/
frameworks/
integrations/
migrations/
api/openapi/
deploy/
web/
docs/
```

All commands use the same Go module, domain, and application packages. Their small `main` functions only supply process arguments and streams to the shared application shell and return its exit code. API, worker, collector, MCP gateway, and replay are process roles, not separate commercial applications.

`internal/domain` provides a shared unavailable-operation error, the [FeatureCatalog](feature-catalog.md), the [Edition model](edition-model.md), the [Entitlement model](entitlement-model.md), and shared JSON-object validation. `internal/entitlement` provides the organization-scoped repository contract, [EntitlementResolver](entitlement-resolver.md), and [snapshot service](effective-entitlement-snapshot.md). `internal/authorization` implements [PermissionService](permission-service.md); `internal/application` provides guarded request orchestration alongside help and explicit non-success exits for unavailable commands. Other service boundaries remain placeholders. Infrastructure test tooling lives under `internal/platform`. Production storage/authentication/quota adapters, license verification, and the verification engine remain unimplemented.

Top-level adapter and delivery directories preserve the shared boundaries. `migrations/` contains one ordered migration history and `api/openapi/` contains one API specification. Go owns backend and verification responsibilities. `web/` now implements the Next.js pricing page and a public catalog read from Go-generated product configuration; dashboard work remains outside the pricing milestone. See the [web setup](../../web/README.md).

## Dependency direction

| Owner | Allowed local dependencies | Prohibited examples |
| --- | --- | --- |
| `internal/domain/**` | Its own domain subtree | Application, billing, licensing, providers, database or network clients |
| `internal/tenant/**` | Tenant and shared domain | Application services and infrastructure |
| `internal/entitlement/**` | Entitlement, tenant, domain | Billing/license adapters and authorization implementation |
| `internal/authorization/**` | Authorization, tenant, domain | Subscription/entitlement implementation and infrastructure |
| Other core packages | Core packages, including shared domain and capability/permission contracts | Application orchestration and infrastructure |
| `internal/application/**` | Application and core packages | Billing, licensing, config loaders, platform clients, or external SDKs |
| `cmd/`, `internal/platform/`, `internal/config/`, `internal/licensing/`, `internal/billing/`, and top-level adapters | Inward-facing contracts and appropriate infrastructure | Edition-specific copies of domain logic |

Core packages are explicitly classified in [the architecture test](../../internal/platform/architecture/architecture_test.go): domain, tenant, entitlement, authorization, verification, evidence, intent, spec, sir, graph, proof, policy, risk, trust, provenance, agent, replay, runtime, and audit. New internal package trees require a policy decision; an unclassified helper is rejected.

Application services will coordinate separate entitlement and permission contracts through injection. Licensing and billing translate validated external inputs into those contracts. No service is implemented in this structural milestone and no edition-to-feature allocation is selected. Software version, edition, and channel remain independent as specified in the [product model](../roadmap/version-edition-matrix.md).

## Architecture enforcement

`go test ./...` runs the tests in `internal/platform/architecture`:

- Parse every Go source file in protected package trees, including nested packages, generated files, test files, platform-specific filenames, and files excluded by build tags.
- Enforce an explicit allowlist of standard-library imports and approved inward dependencies. This rejects Stripe, PostgreSQL, NATS, GitHub, arbitrary third-party SDKs, and internal infrastructure adapters. Standard-library infrastructure such as `database/sql`, `net/http`, `os`, and `os/exec` is also rejected in core/application code.
- Match full import paths, including aliased, dot, and blank imports. Check every allowed core helper as well, so routing an infrastructure dependency through a helper does not evade the rule.
- Reject unclassified internal trees, edition-named directories, nested Go modules, and `go.work` files. Reject source symlinks rather than silently skipping their targets. Root Git/dependency directories and generated web dependency/build directories are excluded; similarly named directories inside core code are still inspected.
- Fail on traversal, read, and parse errors. Permit `testing`, `testing/fstest`, and `testing/quick` only in test files. Place tests needing real infrastructure in adapter packages.
- Check that all requested architectural directories remain present.

Negative fixtures exercise the scanner without installing prohibited SDKs. They cover provider imports, standard-library infrastructure, unknown providers, helper indirection, build-tag/generated/test bypasses, malformed source, unreadable filesystems, and symlinks. Additional process integration tests build and execute all six commands and assert help and unavailable-operation behavior. The race run instruments the Go test packages; the integration test's child executables are built normally.

The policy is a source dependency guard, not a sandbox or complete semantic security analysis. It cannot prove tenant isolation, prevent malicious changes to the policy itself, inspect arbitrary external module replacements, or detect every edition comparison embedded in business logic. Its standard-library allowlist is intentionally conservative; expand it with a rationale and negative tests when a real domain need appears. Filesystem link fixtures do not establish Windows junction race safety for future repository execution.

## Validation and scope

Run `go test ./...`, `go test -race ./...`, and `go vet ./...`. No database migration or premium feature is added, so migration and premium access/data-preservation tests remain requirements for their implementing milestones. No persistent user data is modified.

Validated on 2026-09-05 with Go 1.26.5, Windows/amd64:

| Check | Result |
| --- | --- |
| `go test ./...` | Passed, including architecture fixtures and all six executable entry points |
| `go test -race ./...` | Passed after retrying outside the sandbox |
| `go vet ./...` | Passed outside the sandbox |
| `gofmt -l cmd internal` | No unformatted files |
| Markdown relative file links | Passed |

The first sandboxed race/vet attempts failed because Go's build cache returned access denied. These were environment failures; the successful retry used the same source. Packages containing only boundary documentation have no behavioral tests and no implemented services.

The module path is `safepatch` because a hosted import path has not been supplied. There is one module and no new Git repository; the inherited parent Git root remains an operational limitation. Establish that boundary before publishing or broad Git operations.

Next recommended milestone: define tenant-scoped domain identities and separate entitlement/permission contracts, with fail-closed decisions and entitlement/authorization boundary tests. Preserve these import rules; defer engines, billing providers, and storage integrations until explicitly requested.

Related: [one-codebase editions](one-codebase-editions.md), [threat model](../security/threat-model.md), [version/edition/channel matrix](../roadmap/version-edition-matrix.md).
