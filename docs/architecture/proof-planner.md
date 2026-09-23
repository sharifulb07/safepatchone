# ProofPlanner

`internal/proof.ProofPlanner` is one shared, deterministic planning engine. It
selects required work from a trusted capability profile and provider catalog,
without importing edition, entitlement or authorization models. No edition-specific
planner, verifier, binary, bundle grant or database schema is introduced.

## Proof levels

| Level | Requirement category |
| --- | --- |
| L0 | Syntax |
| L1 | Build |
| L2 | Tests |
| L3 | Security |
| L4 | Semantic |
| L5 | Behavioral |
| L6 | Adversarial |
| L7 | Runtime |
| L8 | Formal |

Levels are cumulative planning requirements: targeting L3 includes every configured
L0–L3 obligation. A profile must define at least one obligation for every level;
multiple obligations at one level are supported. Missing capability access never
silently reduces the target. These categories do not claim that every required
provider has been implemented, or that stronger proof follows from scheduling work.

`DefaultObligations` declares `proof.syntax` through `proof.formal` as **technical
capabilities**, not feature grants or executable providers. It registers no tools.
An empty provider catalog returns a BLOCKED plan with explicit unavailable gaps.
Trusted composition can map obligations to existing SDK capabilities, such as
`go.build`, `go.test`, and `go.govulncheck`, while preserving the same planner.
Profile adequacy is a policy responsibility; the planner does not invent a formal
verification engine or infer that a test covers a business property.

## Planning contract

Construct `NewProofPlanner(version, obligations, providers)` with trusted,
release-pinned configuration. Each provider declares ID, version, one technical
capability, priority and explicit upper-bound resource costs. Changing configuration
creates a new configuration hash. Caller-owned slices cannot mutate a constructed
planner. Provider IDs identify concrete execution routes owned by composition;
they need not equal a verifier ID because one verifier can expose many operations.

The core request pins tenant, repository, immutable source revision and target. It
also contains readiness/access states and remaining budgets supplied by trusted
application admission, never by repository content or request JSON. Missing provider
states are UNKNOWN. Missing resource budgets allow zero consumption. Empty cost
lists explicitly describe unmetered work; there are no inferred paid-plan prices.

Selection proceeds in level/obligation-ID order, then provider priority/ID order.
The first available provider whose full cost fits the remaining budget is selected.
An unavailable, denied or unaffordable provider can be replaced by another configured
provider for the same capability. Repeated obligations for the same capability reuse
one task and charge it once. Costs accumulate across distinct selected tasks, and
checked arithmetic prevents overflow. This is bounded greedy selection, not a
global optimization solver; it can report a budget gap even if a different earlier
choice would yield a feasible complete plan.

The sealed `Plan` includes its schema/configuration version, tenant, pinned subject,
target, selected provider versions/costs, obligation resolutions and total estimates.
It has canonical JSON and a content hash. JSON decoding cannot overwrite a sealed
plan; trusted composition replans from its stored inputs rather than accepting a
client-supplied plan as authority. Identical configuration and decisions produce
identical hashes. Hashes are identity checks, not signatures or approvals.

| Resolution | Meaning |
| --- | --- |
| `SELECTED` | Work selected for the obligation |
| `CAPABILITY_DISABLED` | All otherwise relevant access paths were disabled |
| `PROVIDER_UNAVAILABLE` | No matching usable implementation was supplied |
| `PROVIDER_UNKNOWN` | Required readiness/access information is incomplete |
| `BUDGET_EXCEEDED` | Available matching work does not fit the remaining budget |

A plan is `READY` only when every requested obligation has selected work. Otherwise
it is `BLOCKED`; partial selections are retained for explanation, with no implied
dispatch authority. Neither state is a verification verdict. Plans never contain
an achieved proof level or PASS. Do not copy the target level into policy engine
`Input.ProofLevel`. Actual evidence evaluation and trusted approval are separate.

## Application admission and quotas

`application.ProofPlannerService.Plan` accepts only the pinned subject and target
after authentication supplies the actor and tenant scope. It reuses `ExecutionGuard`
to resolve repository ownership, require `proof.planner`, check `proof.execute`,
and apply the existing planning-operation quota hook.

Every provider has a trusted `ProofProviderBinding`. The service checks every
configured prerequisite through the existing FeatureCatalog/EntitlementService,
then independently checks the provider-specific actor permission. Bindings can
include `graph.organization`, `graph.cross_repo`, `intent.advanced`, `runtime.replay`
or another existing key as appropriate. Such keys do not change planner semantics.
Use existing mappings such as `GoVerificationRequirement` when configuring Go work.
Organization data sources remain responsible for resolving every repository they
read; a grant does not authorize arbitrary cross-tenant data access.

Only admitted providers needed through the target level are passed to the trusted
`ProofProviderSource.Check` readiness adapter. It must verify real implementation,
tool/data availability and pinned version for the requested subject. Registration,
an enabled grant or an LLM suggestion alone is insufficient. Future providers stay
UNAVAILABLE or UNKNOWN. The source receives defensive copies, so it cannot alter
configured costs. Exceptions returned as errors are redacted by stage; lookup errors
fail the complete request rather than returning a misleading partial success.

The service reads applicable current limits and usage in one organization-scoped
QuotaStore transaction. It counts both reserved and committed units for the current
UTC month. INTEGER, DISABLED and UNLIMITED use the existing limit model, including
overflow headroom. Plan estimates support the existing monthly proof-credit,
replay-request and fuzz-second resources. No numeric edition limits are added.
Concurrency, retention and other reusable capacity remain dispatch/storage policy;
they must not be summed as consumable costs across a plan.

Planning reads execution budgets without reserving or settling them. The planning
operation has its own injected quota hook, separate from future execution charges.
Quota estimates are advisory and can expire immediately due to other work or plan
changes. At actual worker start, authoritative composition must recheck provider
grants, permissions and readiness, atomically Reserve all required execution quotas,
and carry reservations through Commit/Release. Reuse existing guards, QuotaService,
verifier lifecycle and secure Runner. A READY plan is never an execution lease.

Revoking grants or lowering limits changes subsequent plans without changing prior
sealed plans, evidence, reservations or accounting history. Restoring access can
select the same preserved configuration again. This requires no migration.

## Bounds, tests and remaining scope

Shared safety bounds allow 128 obligations, 256 providers and 16 distinct resource
dimensions. The planner runs bounded loops, honors cancellation, rejects unknown
levels/duplicate identities/invalid costs and uses portable identifier validation.
It executes no commands, follows no paths, fetches no URLs and evaluates no prompt
or scanner text as code. API output must still be rendered as untrusted text.

Tests cover cumulative levels, deterministic fallback, task reuse, immutable and
concurrent planning, overflow, malformed requests, grants, independent permissions,
provider readiness, quota usage and downgrade/restore across all five edition labels.
A component integration test feeds selected Go capabilities into the existing Go
verifier and checks that it preserves secure Runner policy. Architecture tests
reject commercial imports in the shared proof engine.

Run `go test ./...`, `go test -race ./...` and `go vet ./...`. Existing migration
tests continue to cover the unchanged shared schema; their native PostgreSQL portion
requires the documented opt-in database/psql configuration.

This milestone does not add installed-provider discovery adapters, executable
formal/runtime engines, a scheduler, proof-level certification, persistent plan
storage, HTTP/CLI routes or production worker wiring. Concrete profiles, readiness
adapters and execution cost bounds must be supplied by trusted composition before
exposing the planner in a product workflow.
