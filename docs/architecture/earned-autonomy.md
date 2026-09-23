# Proof-based agent capability escalation

`agent.AutonomyCompiler` deterministically evaluates operator-reviewed `AutonomyPolicy`
and authoritative proof records. `application.EarnedAutonomyService` issues and
uses bounded SafeLeases through the existing MCP gateway, Intent Capability
Compiler, TemporalGuard, quota hooks and lease stores. All editions use these
same types and execution code. No edition name is consulted by the kernel.

## Server integration

Compose `NewEarnedAutonomyService(existingGateway, trustedAutonomySource)`.
The source must load operator-approved policy and retained verifier results,
including their actor and challenge bindings. It is a privileged server adapter;
implementing it from request bodies, uploaded Passports, prompts, repository
policy files or MCP output would violate the trust boundary.

1. Authenticate with the shared identity system and select the owned repository.
2. Call `Challenge` with a reviewed policy ID, Intent and exact proposed calls.
   This returns an input digest and grants no execution authority.
3. Run the shared verification pipeline for that proposal. Retain the actor and
   challenge in verifier-owned input provenance alongside the resulting Passport.
4. The source returns only current, unrevoked records for that binding. A signed
   Passport alone does not establish actor/challenge binding or current validity.
5. Call `Issue` to obtain an earned SafeLease. The complete lease duration must
   fit within the accepted proof's validity and policy freshness window.
6. Use this service's `Invoke` with the original proposal and selected call.
   It reloads proof, policy, inventory and access, then uses the gateway's shared
   atomic consumption and dispatch. The ordinary gateway rejects earned leases.

The kernel requires distinct verification runs with PASS verification and PASS
policy decisions at the reviewed minimum proof level. It rejects UNKNOWN, stale,
future, missing, duplicate, foreign-tenant, foreign-actor or mismatched evidence.
The challenge binds the organization, repository, actor, Intent, exact argument
and tool-definition digests, reviewed policy and effective call budget. A proof
set digest also pins accepted Passport bytes and their validity deadlines.
Replacing evidence or changing authority requires a fresh lease.

Only existing `READ` and `WRITE_NONPRODUCTION` rules can be compiled. Operators
must verify that upstream tool behavior matches that classification. Proof and
natural-language intent cannot authorize production or add unreviewed tools.
MCP responses remain untrusted output; RETURNED does not mean verification PASS.

## Entitlements and limits

Admission requires `agent.earned_autonomy` plus the existing inventory, gateway,
SafeLease and TemporalGuard capabilities. Independent `agent.manage` and
`agent.approve` permissions must pass, including for service accounts. Current
tenant ownership is checked first. The feature's `Limit` is the maximum total
invocations per earned lease, intersected with operator policy: 1 through 32.
Missing, zero, invalid or unavailable limits deny access. The hard ceiling is a
resource safety bound; it is not an edition rule. Both the autonomy operation
and shared gateway dispatch use the existing quota admission hooks.

`config.EarnedAutonomyEditions` adds sealed `enterprise-2026-v6`, granting
`agent.earned_autonomy` with limit 32 and the missing inventory prerequisite.
Historical bundles, customer subscriptions, licenses and resource usage remain
unchanged. A trusted provisioner must explicitly assign the new bundle.
Future Business allocation needs only a new immutable bundle with this feature,
a smaller numeric limit and the gateway prerequisites. No kernel or binary change
is required. Product semantic version and release channel remain independent.

Lost entitlement, lost permission, exhausted quota or changed proof/policy blocks
further use. History remains readable through the existing authorized store;
revocation remains available after downgrade. Re-enablement does not reset uses
or resurrect revoked/expired leases. Consumption is recorded as UNKNOWN before
dispatch and never refunded or automatically retried on uncertain MCP outcomes.

## Persistence and validation

The optional `earned` binding extends `safepatch.agent.capabilities.v1` for every
edition; ordinary capability encodings remain byte-identical. It is retained
inside the existing immutable `agent_safe_leases.capabilities` text, including
in PostgreSQL. Migration 000020 adds binding validation and seeds the new sealed
bundle without changing old leases or assignments. Older binaries fail closed
on the unknown binding; upgrade gateway nodes before enabling issuance.

Tests cover kernel proof rejection, shared dispatch, every edition allocation,
independent authorization, tenant isolation, call budgets, downgrade history,
policy/inventory/proof drift, invalid bindings and configuration-only reassignment.
Migration tests compare embedded JSON and include an opt-in native PostgreSQL
fixture for history preservation, malformed binding rejection and immutability.

This milestone exposes a Go application service. It does not add a public HTTP
autonomy endpoint, install a production `AutonomySource`, or claim production
rollout. A deployment must implement authoritative verifier provenance storage
and policy approval before enabling it. Existing gateway limits also apply:
upstream tool classification is trusted, and authorization/revocation changes
cannot cancel a call already dispatched. Reads use bounded contexts; there is no
distributed transaction spanning entitlement providers, proof storage and tools.

## Milestone file inventory

Inspected the shared agent compiler, inventory, SafeLease and TemporalGuard;
`internal/application/agent_gateway.go`; authorization permissions; memory and
PostgreSQL lease stores; the Passport schema; edition update loaders; migration
000013 and 000019; existing gateway, configuration, migration and architecture
tests; OpenAPI; and the v1.0 readiness blocker register.

Created:

- `internal/agent/trust_kernel.go`
- `internal/agent/trust_kernel_test.go`
- `internal/application/earned_autonomy.go`
- `internal/config/earned_autonomy_bundles.go`
- `internal/config/earned_autonomy_bundles.json`
- `internal/config/earned_autonomy_bundles_test.go`
- `internal/platform/mcpgateway/earned_autonomy_test.go`
- `internal/platform/migrationtest/earned_autonomy_test.go`
- `internal/platform/migrationtest/earned_autonomy_before.sql`
- `internal/platform/migrationtest/earned_autonomy.sql`
- `migrations/000020_earned_autonomy.up.sql`
- `docs/architecture/earned-autonomy.md`

Modified:

- `internal/agent/compiler.go`
- `internal/application/agent_gateway.go`
- `internal/domain/feature/builtin.go`
- `internal/domain/feature/builtin_test.go`
- `api/openapi/openapi.yaml`
- `migrations/README.md`

## Validation on 2026-09-07

- Final `go test ./...`: PASS.
- Final `go test -race ./...`: PASS; no race reports.
- `go vet ./...`: PASS.
- Shared OpenAPI YAML parsing and earned-binding schema check: PASS.
- Migration/configuration parity: PASS. Native PostgreSQL fixture: SKIP;
  `SAFEPATCH_POSTGRES_MIGRATION_TEST=1` and libpq configuration were absent.
- An earlier full run reproduced existing readiness blocker B01 at
  `downloads_test.go:178` (`restored grant inaccessible`). Subsequent passing
  runs do not resolve that intermittent protected-download failure. No download
  implementation or test was changed and no release was performed.

Next recommended prompt: implement a trusted `AutonomySource` using retained
verification input provenance and operator-approved policy in the existing host,
then validate issuance and invocation against PostgreSQL with real verifier
results. Preserve the same gateway and AutonomyCompiler and keep release blockers open.
