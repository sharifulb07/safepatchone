# Final technical authorization

`internal/trust.TrustKernel` is the single deterministic final technical decision
kernel. It receives no organization edition, subscription, entitlement service,
commercial limit, price or release channel. Architecture tests prohibit imports
of commerce and admission packages. Exactly the same implementation is called
by the shared local and hosted change workflow.

Existing PASS/FAIL/UNKNOWN results remain diagnostic verification outcomes in
Passports and GitHub Checks. A passing build is not production authorization.
The workflow now retains a separate `safepatch.trust-decision.v1` result in change
artifacts and returns its decision in `ChangeResult.Authorization`. Policy
evaluation is delegated to the kernel, which reuses the existing policy engine.
The prior agent-specific helper is now named `agent.AutonomyCompiler`; it keeps
its existing bounded-lease behavior. `trust.TrustKernel` owns final technical
authorization rather than commercial capability allocation.

## Inputs

| Input | Meaning and trust boundary |
| --- | --- |
| Intent | Existing immutable IntentContract. Natural-language text is inert. Every observation must bind its content hash in `input_hashes.intent`. |
| Evidence | Existing immutable Evidence records; at most 256 observations and 16 MiB aggregate canonical bytes. Every record must match the organization, change and run, including observations not selected by a policy rule. Duplicate IDs, future records and substituted inputs are rejected. |
| ProofLevel | Achieved existing L0–L8 level, supplied by trusted orchestration. UNKNOWN requests re-verification; a planner target alone is not achieved proof. |
| Risk | Trusted assessed integer 0–100; nil is unknown, never zero. The kernel does not calculate risk from prose. |
| Policy | Existing immutable policy plus trusted technical Controls: minimum proof level, maximum proof age, risk review threshold and impact escrow threshold. Policy is evaluated inside the kernel, not accepted as a caller-provided PASS. |
| ProofCoverage | Trusted required obligation IDs mapped to retained evidence IDs. Empty obligations, missing observations or incomplete extraction cannot establish completeness. Maximum 128 obligations and 1,024 evidence links. |
| BusinessImpact | Existing LOW/MEDIUM/HIGH/CRITICAL policy criticality, or explicit UNKNOWN. This is technical impact, not a commercial edition. |
| ProofFreshness | Trusted verification and expiry times, evaluated against an explicit AsOf time and policy age bound. Both evidence age and verification age matter; expiry is exclusive. |
| Provenance | Trusted attestation-verification result bound to change, run, Intent, policy and the entire evidence set. VERIFIER is the only accepted origin. An LLM origin is rejected even if its supplied status says PASS. |

An evidence hash authenticates no producer by itself. Provenance status must come
from a trusted adapter that validates retained verifier provenance and its trust
root, never from an uploaded document or scanner/MCP output. The kernel performs
no network access, subprocess execution, key discovery or signature verification.
Its assessment source is responsible for those deployment-specific trust checks.

## Decision semantics

Decisions are ordered by the following precedence; a weaker condition cannot
hide a stronger one:

| Output | Trigger |
| --- | --- |
| BLOCK | Invalid input/context, failed evidence or policy, rejected/LLM provenance, subject/digest substitution or invalid time chronology. |
| REVERIFY | Missing/incomplete proof, coverage, freshness or verified provenance; stale proof; insufficient achieved level; unresolved policy requirements. |
| ESCROW | Complete acceptable proof with business impact at or above the trusted escrow threshold. Hold for governed release. |
| REVIEW | Otherwise acceptable proof with unknown risk/impact or risk above the trusted review threshold. |
| AUTHORIZE | Every technical requirement is satisfied and no hold/review condition applies. |

Known failure remains BLOCK even when another observation is UNKNOWN. REVIEW,
ESCROW and REVERIFY are never permission to proceed. AUTHORIZE is a technical
verdict, not a bearer token or a permission grant. Actor authorization, tenant
resolution, current deployment controls and operational approval still apply at
the action boundary. No automatic merge, deployment, review queue or escrow
executor is introduced in this milestone.

Results include bounded sorted reason codes and an input digest covering subject,
run, all nine technical inputs, controls and evaluation time. Input collections
are normalized on copies; evidence/coverage order does not change the digest.
Evaluation reads no wall clock or global mutable state. Caller-owned mutable DTOs
must not be changed concurrently during evaluation.

## Shared workflow integration

The existing execution guard still resolves entitlements and independent actor
permissions before verifiers execute. Proof level is derived only after all
planned observations pass. The workflow binds generated evidence to Intent,
builds coverage from completed plan steps, and calls the kernel.

Hosts may install `ChangeWorkflowConfig.FinalAuthorization` with trusted Controls
and an `AuthorizationAssessmentSource`. The source receives the exact change,
run, Intent/evidence/policy digests and returns authoritative risk, impact,
freshness and provenance verification. Its context is capped at ten seconds.
The constructor copies configuration; no edition is passed to the kernel.

Without this source, assessment fields remain explicitly unknown and the result
cannot be AUTHORIZE. Default controls require the workflow target level, a
ten-minute proof age, risk review above 50 and escrow for HIGH/CRITICAL impact.
These defaults are security policy, not edition allocation.

Final results are returned only after durable artifact persistence and integrity
checks. Any workflow, persistence or audit error returns BLOCK for final
authorization. Stored verdicts describe their recorded input/time; consuming
systems must revalidate current state instead of replaying an old AUTHORIZE.

## Compatibility and validation

No database migration or entitlement bundle change is needed. Existing immutable
artifacts and Passports remain unchanged. New change artifacts add the separate
authorization result; historical records without that field contain no final
authorization. The shared OpenAPI specification describes the new result schema.
Old evidence lacking an Intent binding must be re-verified before it can satisfy
the new kernel.

Tests cover every outcome, precedence, exact thresholds, missing/invalid inputs,
tenant/run/Intent/provenance substitution, inert injected payloads, cancellation
at every checkpoint, deterministic concurrency, and bounded aggregate work.
All five actual edition configurations run through identical local/hosted
workflow fixtures. Tests prove denied entitlements and actor permissions stop
execution, and re-enablement preserves prior artifacts and technical judgments.
Persistence failure never returns an authorization.

`FuzzTrustKernel` exercises typed technical inputs and fail-closed/monotonic
properties. `FuzzTrustKernelUntrustedDocuments` exercises immutable Evidence,
Intent and Policy decoders and substitution rejection. Fuzzing is bounded to two
workers here; it is empirical testing, not a formal correctness proof.

Remaining deployment work: install production assessment/provenance adapters and
route technical holds through the existing operational governance system. This
milestone does not provide new risk/impact inference, production signing trust
roots, commercial provisioning, or permission to release while readiness blockers
remain.

## Task inventory

Inspected `internal/trust`, the agent compiler and autonomy service, shared change
workflow/archive, policy engine, proof planner/Passport, Evidence and Intent
contracts, admission guards, configuration, migration conventions, integration
fixtures and OpenAPI.

Created:

- `internal/trust/kernel.go`
- `internal/trust/kernel_test.go`
- `internal/trust/kernel_fuzz_test.go`
- `internal/application/final_authorization.go`
- `internal/platform/githubapp/trust_kernel_integration_test.go`
- `internal/platform/architecture/trust_kernel_test.go`
- `docs/architecture/trust-kernel.md`

Modified:

- `internal/trust/doc.go`
- `internal/application/change_workflow.go`
- `internal/agent/trust_kernel.go`
- `internal/agent/trust_kernel_test.go`
- `internal/application/earned_autonomy.go`
- `docs/architecture/earned-autonomy.md`
- `api/openapi/openapi.yaml`

## Validation record, 2026-09-07

- `go test ./...`: PASS.
- `go test -race ./...`: PASS; no reported data races.
- `go vet ./...`: PASS; gofmt check clean.
- Shared OpenAPI YAML and the exact five output values: PASS.
- Every function in `internal/trust/kernel.go`: 100% statement coverage.
  The entire trust package, including older diagnostic/self-approval code,
  measured 91.3%; this is not a claim of formal correctness.
- Final `FuzzTrustKernel`, 20-second budget, two workers: PASS, 16,229 executions.
- Final `FuzzTrustKernelUntrustedDocuments`, same budget: PASS, 79,846 executions.
  Both targets were also exercised earlier during implementation.
- No SQL migration was introduced or applied. Native PostgreSQL validation is
  still subject to the repository's opt-in integration environment; passing the
  Go suite is not evidence of a live database or production deployment test.

Next recommended prompt: wire trusted risk, business-impact and provenance
assessment adapters into the existing host, then exercise REVIEW/ESCROW handling
and re-verification at the operational action boundary using this same kernel.
