# Verification integrity across editions

SafePatch now has three shared protections: `VerificationIntegrity`, `PolicyGuard`
and `SelfApprovalGuard`. They have no edition input, feature entitlement or disable
switch. The existing operation still requires its own entitlement, actor permission
and quota admission; once available, its integrity boundary is mandatory.

## VerificationIntegrity

`internal/verification/integrity.go` compares immutable normalized snapshots with
organization, repository and pinned revision identity. Controls contain scanner
and test capability IDs, enabled states, opaque configuration digests, a coverage
threshold in basis points, timeout in milliseconds, security exclusions and the
existing shared policy document.

The six requested changes are detected:

| Change | Finding |
| --- | --- |
| Enabled scanner disappears or is disabled | SCANNER_REMOVED |
| Enabled test capability disappears or is disabled | TEST_DISABLED |
| Coverage threshold is lowered or removed | COVERAGE_THRESHOLD_REDUCED |
| Baseline policy obligations become weaker | POLICY_WEAKENED |
| A new security exclusion appears | SECURITY_EXCLUSION_ADDED |
| Verification timeout decreases | VERIFICATION_TIMEOUT_REDUCED |

Changing an existing enabled control's opaque configuration digest is unclassified
and produces UNKNOWN, not an invented equivalence or permission to execute.
Incomplete extraction also prevents INTACT. Known weakening produces BLOCKED;
neither BLOCKED nor UNKNOWN can proceed through the workflow, even with a review.
This milestone has no override for weakened controls.

The snapshot format is `safepatch.verification-controls.v1`; the diagnostic result
is `safepatch.verification-integrity.v1`. Configuration identity excludes only the
revision, so ordinary source revision changes do not themselves require a control
review. Full snapshot digests still bind each revision. Both formats are identical
across Community, Developer, Team, Business and Enterprise.

Decode rejects duplicate keys, unknown fields (including disable switches), case
aliases, invalid control IDs/digests, ambiguous nulls and excessive input. Bounds
are 1 MiB, 256 scanners, 4096 tests, 256 exclusion strings and a 24-hour maximum
timeout. Exclusions are compared as inert strings; no glob, regex or path is
executed. Findings use their digests rather than copying exclusion text.

## PolicyGuard

`internal/policy/guard.go` reuses the immutable `policy.Policy` model. It requires
each old rule to remain under its stable ID, its applicability to cover at least
the old repositories/criticalities, and its obligations to be no weaker. It checks
maximum risk, minimum proof level, required evidence producer/type, confidence
and determinism. Adding rules or strengthening requirements is allowed. Renaming
or redistributing equivalent rules is conservatively blocked because semantic
equivalence has not been established. The guard does not silently change the
policy engine's semantics or approve a new active policy.

## SelfApprovalGuard

Non-weakening configuration changes require an independent trusted human review.
`internal/trust/self_approval.go` binds the review to ChangeID, both control digests,
a stable review ID and a validity window of at most 24 hours. The reviewer cannot
be the requesting actor or any member of the complete author set. Unknown authors,
expired/future reviews, reused change identities and altered controls are denied.
LLM and service-account origins cannot provide that human review.

Workflow admission resolves the reviewer's current tenant access and checks
`PermissionService.Allowed(..., policy.manage)` independently. It does not require
the commercial central-policy feature just to enforce this safety boundary.
Review DTOs are not credentials. A trusted source must obtain immutable reviews
from authenticated server records and check revocation on each read. Source or
scanner text claiming `origin: HUMAN_SESSION` must never become such a record.

## Mandatory shared workflow integration

`ChangeWorkflowConfig.Integrity` is now required. Existing compositions must supply
a `VerificationIntegritySource`; missing adapters fail construction rather than
turning off protection. `Run` and `RunHosted` follow the same path:

1. Admit the underlying operation using existing tenant, entitlement, permission
   and quota checks.
2. Load protected baseline and proposed controls for the exact organization,
   repository, base SHA and head SHA. Reject missing, foreign or stale data.
3. Require the configured evaluator policy hash to equal the protected baseline
   policy hash. The proposed policy cannot evaluate or approve itself.
4. Reject weakening and incomplete extraction. For other control changes, require
   independent review and current reviewer authorization.
5. Require all enabled baseline scanner/test capability IDs in the ready proof
   plan, and enforce the baseline timeout floor in both the initial execution
   request and every request emitted by an SDK verifier to the Runner.
6. Refresh controls and review before each verifier and before artifact publication.
   Digests and review identity must stay pinned; revocation or drift blocks progress.

Successful artifact bundles include the integrity result and, when applicable,
the reviewed control change's review ID. Both control digests and the full review
digest enter each verifier evidence record's input hashes, binding them through
the existing EvidenceGraph and Passport. Rechecks pin the full review digest,
not just its ID. No production authorization is created.
Blocked runs return an error with UNKNOWN workflow decision and cannot publish a
successful GitHub Check through this core. Existing evidence/Passports are not
rewritten. The current failure path does not persist a separate blocked-integrity
audit artifact; diagnostics remain available from the shared guard result.

The protected baseline governs the change's verification. Approval of a stronger
proposed configuration does not activate it in the control plane. Policy approval,
activation and deployment remain separate operations.

## Trusted extraction and remaining boundaries

This milestone implements normalized comparison and mandatory workflow enforcement,
not a universal parser for CI YAML, shell scripts or arbitrary verification code.
The source adapter must cover every effective verification surface and bind tool,
code and execution-policy identity into control digests. Unsupported changes must
produce incomplete extraction or a changed opaque digest, never a synthesized
unchanged baseline. Reuse TestTrust extraction when deriving disabled-test facts;
the integrity model's test entries correspond to capabilities required in the plan.

The shared workflow currently has an injected source boundary and integration
fixtures, not a deployed protected-configuration/review store. Wiring that adapter
requires authoritative repository/control-plane data. Reviews are rechecked before
dispatch/publication, but this does not claim a transaction across the external
review system, execution and object storage. Credentials, review provisioning,
physical artifact retention and deployments remain infrastructure responsibilities.

No migrations or dependencies are added: existing artifact persistence stores the
extended bundle, and no new mutable database entities are introduced. The single
OpenAPI specification adds control, integrity and review components; no public
approval endpoint is introduced. Edition bundles and limits remain unchanged.

## Validation and inventory

Unit/security/error tests cover all six detectors, policy requirements and selector
changes, malformed controls, immutable snapshots, unknown extraction, human review
identity, authorship, expiry and replay binding. Integration tests exercise local
and hosted execution under every edition label with only the basic operation
grant, missing plan capabilities, actual timeout reduction, self-approval, reviewer
permission denial, review replacement/revocation and preserved historical artifacts.

```powershell
$env:GOCACHE = Join-Path $env:TEMP 'safepatch-go-build'
$env:GO_TELEMETRY_CHILD = '2'
go test ./...
go test -race ./...
go vet ./...
go test ./internal/verification -run '^$' -fuzz FuzzVerificationControls -fuzztime 5s -parallel 2
```

Validation on 2026-09-07: final full Go tests, race tests and `go vet ./...`
passed, including control/review digest binding through evidence. The bounded
decoder fuzz run passed with 27,824 executions. Existing migration contracts
passed; native PostgreSQL execution was skipped because its opt-in database
environment is not configured. Deployment adapter and live GitHub validation
remain outstanding.

Inspected: shared ChangeWorkflow and GitHub integration fixtures; policy model,
engine and management service; trust decisions; ExecutionGuard; authorization;
Runner; TestTrust; artifact contracts and architecture rules.

Created: `internal/policy/{guard,guard_test}.go`,
`internal/verification/{integrity,integrity_test}.go`,
`internal/trust/{self_approval,self_approval_test}.go`,
`internal/application/verification_integrity.go`,
`internal/platform/githubapp/integrity_integration_test.go`, and this guide.
Modified: `internal/application/change_workflow.go`,
`internal/platform/githubapp/core_integration_test.go`, `api/openapi/openapi.yaml`.
Migrations: none.

Next prompt: "Implement the protected VerificationIntegritySource adapter using
authoritative baseline configuration and authenticated review records, then validate
weakening attempts through the deployed GitHub workflow end to end."
