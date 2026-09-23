# Shared agent controls

SafePatch now implements MCP inventory, a deterministic Intent Capability
Compiler, MCP Gateway, SafeLease and TemporalGuard in the shared Go architecture.
All editions use the same types, compiler, store, transport and gateway. No
Enterprise build, edition branch or alternate migration stream is introduced.

## Enforcement flow

1. Authenticate the existing session bearer and resolve organization/repository
   ownership through the existing organization repository.
2. Check current organization entitlements, independent `agent.manage`
   permission and the configured bounded-operation quota hook.
3. Discover tools from deployment-bound MCP servers and load trusted policy.
4. Compile explicit tool-call proposals against Intent components, exact reviewed
   arguments and pinned inventory definitions.
5. Issue a short-lived SafeLease bound to the actor, organization, repository and
   immutable capability digest.
6. On invocation, rediscover inventory, reload policy and refresh access.
   TemporalGuard checks the current lease under the store's atomic consumption
   lock. Record UNKNOWN before dispatch; then invoke the tool at most once for
   that invocation ID and record a bounded result digest.

| Component | Implemented behavior |
| --- | --- |
| MCP inventory | Bounded paginated `tools/list`, duplicate/loop rejection, canonical immutable tool definitions, tenant-scoped server bindings. Descriptions, schemas and annotations are inert. |
| Intent Capability Compiler | Intersects exact proposed calls with trusted policy and explicitly allowed Intent components. Forbidden components also block their descendants. All Intent fields are hashed; natural-language statements are not interpreted as permissions. |
| MCP Gateway | Authenticated HTTP orchestration, pinned upstream MCP client, current capability/permission checks, durable consumption before dispatch, bounded output and stable error responses. |
| SafeLease | Actor-bound server record, fixed expiry, finite calls, revocation, per-invocation replay protection and immutable outcome history in PostgreSQL. |
| TemporalGuard | Exclusive expiry, backwards-clock denial, actor/scope checks, inventory/policy digest checks and atomic call-budget enforcement on each invocation. |

The compiler accepts only operator-reviewed `READ` and `WRITE_NONPRODUCTION`
rules. Production, destructive and unknown effects are rejected. A prompt or
tool's `readOnlyHint` cannot authorize a rule. Operators remain responsible for
classifying the actual upstream tool and reviewing the *complete* arguments.
This deliberately uses exact argument digests rather than a partial JSON Schema
validator, wildcard paths, inferred effects or natural-language approvals.

## Entitlements and authorization

| Operation | Required feature keys | Permission |
| --- | --- | --- |
| Inventory, compile | `agent.mcp_inventory` | `agent.manage` |
| Issue lease, invoke | `agent.mcp_inventory`, `agent.mcp_gateway`, `agent.safelease`, `agent.temporalguard` | `agent.manage` |
| Revoke lease | None; removing authority remains available after downgrade | `agent.manage` |

The existing commercial catalog and sealed edition bundles are unchanged. An
organization can receive inventory/compiler observability alone without owning
gateway enforcement. Business or Enterprise assignment is a provisioning choice
using the existing entitlement system. This milestone does not silently enable
features or mutate subscriptions. In particular, existing v0.2 bundles do not
automatically provide the new inventory prerequisite: explicitly provision the
required organization grants before expecting access. Published FUTURE metadata
is not rewritten in an old sealed bundle.

Every invocation resolves authoritative current entitlements. No client lease,
edition field, session role claim, MCP annotation or cached compiler output grants
runtime ownership. Downgrades deny new work and preserve historical leases and
receipts. Service-account identity is carried distinctly from user identity; the
existing configured authentication/membership adapter must support it before
service accounts can be admitted. The composed host currently uses user sessions.

## HTTP and host configuration

The existing `safepatch-api serve` host optionally enables the routes when
`SAFEPATCH_AGENT_CONFIG` points to operator-owned JSON. It reuses the existing
database, organization resolver, authoritative entitlement runtime, permission
service and login-session validator. Apply migration 000013 before enabling it.
The separate `safepatch-mcp-gateway` command remains the existing command shell;
there is one implemented API-host composition, not a second server application.

Start from [the secret-free configuration example](../../deploy/agent-gateway.example.json).
Replace the example organization, repository, endpoint and IP with explicitly
approved values. Empty `rules` permits discovery but **no calls**. Configure a
`token_env` name if the upstream needs credentials; its value is loaded from the
server environment and never returned by the API. The document requires all
declared configuration fields and rejects unknown fields.

```powershell
$env:SAFEPATCH_AGENT_CONFIG = 'C:\safe-config\agent-gateway.json'
# Retain the existing onboarding/API database, NATS, GitHub and session settings.
go run ./cmd/safepatch-api serve
```

Routes use `POST /v1/organizations/{organization}/repositories/{repository}/agent/{operation}`:

| Operation | Body | Response |
| --- | --- | --- |
| `inventory` | `{}` | Canonical inventory |
| `compile` | `{intent: <full IntentContract>, calls: [...]}` | Immutable compiled capabilities; not a lease |
| `leases` | Same as compile | `lease_id`, `expires_at`, `capabilities_digest` |
| `invoke` | `{lease_id, invocation_id, call}` | `receipt` and untrusted MCP `output` |
| `revoke` | `{lease_id}` | `{revoked: true}` |

Each call is `{server_id, tool_name, arguments}`. Authenticate with
`Authorization: Bearer <existing-session-token>`. These routes do not read
cookies, so the browser's ambient cookie is not a substitute for the bearer.
Existing cookie-based onboarding routes retain their Origin and CSRF checks.
TLS termination and the existing API's trusted deployment configuration remain
required. The single [OpenAPI specification](../../api/openapi/openapi.yaml)
defines the bodies and responses.

For example, an authenticated operator can retrieve discovery data without
granting any tool execution:

```powershell
$headers = @{ Authorization = "Bearer $env:SAFEPATCH_API_TOKEN" }
Invoke-RestMethod -Method Post -Uri 'https://safepatch.example/v1/organizations/example_org/repositories/example_repo/agent/inventory' -Headers $headers -ContentType 'application/json' -Body '{}'
```

Trusted provisioning uses `agent.Tool.Digest()` to pin an inspected definition
and `agent.Call.ArgumentsDigest()` to pin the complete reviewed argument object.
Add a rule containing `server_id`, `tool_name`, `tool_digest`,
`arguments_digest`, `component`, and `effect`. The compiler requires that exact
component in Intent's `allowed_components`. Increase policy `revision` when
changing rules, then reload the host. Policy and inventory digest changes deny
existing leases at their next invocation. Explicitly revoke leases when their
authority must remain removed even if old configuration is restored.

## Transport, replay and persistence

The upstream client implements initialization and tool requests for MCP
`2025-11-25`, with session IDs, JSON responses and a bounded finite SSE response
containing one matching JSON-RPC message. It follows the official
[tool discovery/call contract](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
and [Streamable HTTP framing](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports).
It does not implement sampling, elicitation, task resumption, notification
subscriptions or arbitrary server-initiated requests. Unsupported or ambiguous
responses fail closed.

The endpoint is fixed HTTPS with normal certificate/hostname verification.
Connections dial the first explicitly configured IP pin, with no DNS resolution,
environment proxy, redirect, automatic call retry or destination from a request.
Private/loopback addresses are possible only as explicit operator pins; link-local
metadata, unspecified and multicast addresses are always denied. Tool schema
references, icon URLs, output resource links and prompts are never fetched.
No stdio process or shell execution is implemented. Self-signed certificates
are used only by the test fixture's explicit test trust pool.

Bounds: 16 servers per scope, 128 tools total, 16 pages per server, 16 KiB per
tool definition/argument object, 1 MiB discovery total, 32 compiled calls and uses,
600-second maximum lease, 30-second call deadline capped by lease expiry, 64 KiB
tool result, 128 KiB inbound body and 64 concurrent requests per handler.
PostgreSQL limits active leases to 4096 per organization. Calls have no newly
invented billing meter; the composed quota hook explicitly uses these finite
budgets without consuming proof credits.

Migration `000013_agent_gateway.up.sql` adds `agent_safe_leases` and
`agent_invocations`. Composite foreign keys enforce repository tenancy; user
leases reference organization membership. Lease identity, capabilities, expiry
and maximum budget are immutable. Revocation is irreversible and call counts
cannot decrease. Invocation identity cannot be reused; completed results cannot
be rewritten. Update-and-insert consumption happens in one transaction under a
lease row lock. A second process observes the consumed call instead of dispatching
it again. Callback failures roll back the transaction.

The store retains only capability digests, bounded grants, identity, timing and
outcome digests. It does not store raw arguments, tool output, tokens or prompts.
Large evidence/archive retention remains the existing object-store concern.
Inventory is rediscovered and returned; this milestone does not add a historical
inventory UI or snapshot-retention service. The bounded memory adapter is for
local development/tests; it is never selected by the hosted composition.

An upstream timeout, malformed result, cancellation, crash or failed receipt
write leaves durable UNKNOWN. Do not replace a failed invocation ID with a new
one to retry an ambiguous side effect without reconciliation. `RETURNED` means
a well-formed tool response arrived; `TOOL_ERROR` means the tool reported an
error. Neither means PASS. Revocation prevents subsequent admission, but cannot
undo an already dispatched upstream side effect. Remote servers must enforce
their own credential scope and resource bounds; metadata pins cannot prove a
malicious server executed its advertised implementation.

## Validation and remaining boundaries

Tests cover inventory canonicalization, duplicates, prompt poisoning, exact-call
compilation, malformed JSON, expired/revoked leases, backwards clocks, policy and
inventory drift, cross-tenant/actor access, every entitlement boundary, permission
after ownership, downgrade preservation, concurrent budget/replay races, HTTP
lease/invoke flow, TLS MCP initialization, invalid JSON-RPC IDs, redirects,
oversized output and finite SSE. Native SQL assertions cover tenant constraints,
immutable history, replay identity, budget rollback, expiry extension and
unchanged commercial data. Native execution remains an opt-in infrastructure
check; passing ordinary tests does not imply a live PostgreSQL rollout.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/agent -run '^$' -fuzz FuzzInventory -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: full `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Inventory fuzzing passed with 89,898 executions.
`TestAgentGatewayMigrationContract` passed; `TestPostgresAgentGatewayMigration`
skipped because the native PostgreSQL opt-in and libpq connection settings were
not configured. No dependencies were added. The example JSON parsed successfully.

This implementation intentionally denies production/destructive operations and
supports exact reviewed arguments, not general JSON-schema-based argument
constraints or automatic LLM approval. It adds no server-side OAuth credential
broker, MCP stdio executor, broad protocol-proxy compatibility or new agent UI.
Historical lease cleanup and secret rotation need operator lifecycle management.
Distributed expiry/consumption uses the trusted application clock and persisted
last-use floor; it is not a distributed clock synchronization mechanism.

Inspected files: agent boundary; IntentContract; permission service and keys;
execution guard; feature/edition configuration; evidence/JSON helpers; tenant
scope; SQL organization/GitHub stores and migration harness; onboarding host and
sessions; command shell; OpenAPI and architecture tests.

Created files: `internal/agent/{inventory,compiler,lease}.go` and `agent_test.go`;
`internal/application/agent_gateway.go`; `internal/platform/agentstore/{memory,postgres}.go`;
`internal/platform/mcpgateway/{runtime,http_client,handler,configuration}.go` and
their integration/transport tests; migration 000013 and its contract/native
fixtures; the example configuration and this document.

Modified files: `internal/agent/doc.go`,
`internal/platform/onboardinghost/server.go`,
`internal/platform/onboardinghttp/sessions.go`, `api/openapi/openapi.yaml`.

Next prompt: "Run the native PostgreSQL migration and multi-process SafeLease
smoke tests against a controlled MCP server; verify revocation, ambiguous
side-effect reconciliation and configured organization entitlements."
