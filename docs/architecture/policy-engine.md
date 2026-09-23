# SafePatch policy engine

`internal/policy.Engine` evaluates one versioned declarative policy against a
trusted repository/run context. Rules and results have identical semantics in all
five editions. The engine has no edition, licensing, entitlement or permission
dependency; architecture tests enforce this boundary.

## Policy format

```json
{
  "schema": "safepatch.policy.v1",
  "policy_id": "critical-repository",
  "tenant_id": "org",
  "version": "1",
  "rules": [
    {
      "id": "critical-go-checks",
      "when": {
        "repository_ids": ["repo"],
        "criticalities": ["HIGH", "CRITICAL"]
      },
      "require": {
        "max_risk_score": 30,
        "min_proof_level": 2,
        "evidence": [
          {
            "producer": "go",
            "type": "go.test",
            "min_confidence": 0.8,
            "deterministic": true
          }
        ]
      }
    }
  ]
}
```

These thresholds are example configuration, not commercial defaults. Policy
versions are bounded opaque identifiers, independent of product versions.
Policy documents are tenant-bound and require at least one nonempty rule.
Repository and criticality lists are OR sets individually and AND together.
An empty `when` object applies to every repository in the policy's tenant.

`policy.New(Document)` validates and seals an independent copy;
`policy.Decode([]byte)` validates JSON before sealing. `Policy.Document()` returns
a defensive copy. `json.Marshal` serializes the sealed configuration;
`json.Unmarshal` cannot overwrite it. The policy hash is SHA-256 over
`safepatch.policy.v1`, a newline and canonical configuration JSON. Declaration order
is preserved and is part of policy identity. The hash is not a signature.

The JSON decoder rejects duplicate keys at any depth, case aliases, unknown fields,
explicit null fields, invalid Unicode, excessive nesting, trailing documents and
oversized input. Optional fields are omitted instead of null. There are no scripts,
shell commands, interpolation, regex selectors, remote imports or executable
expressions. Evidence payloads and scanner/LLM prose never become policy code.

## Input and evaluation

```go
configured, err := policy.Decode(policyJSON)
if err != nil { return err }
result, err := (policy.Engine{}).Evaluate(ctx, configured, policy.Input{
    TenantID: organizationID, RepositoryID: repositoryID,
    ChangeID: changeID, RunID: runID, AsOf: evaluationTime,
    Criticality: policy.CriticalityHigh,
    RiskScore: &riskScore, ProofLevel: &proofLevel,
    Evidence: authorizedEvidence,
})
// Handle err; result is an assessment, never production authorization.
```

Risk scores are known integers from 0 through 100. Proof levels are configured
`uint32` ordinals; this milestone defines no named proof hierarchy. Nil risk/proof
pointers mean UNKNOWN; known zero is different from missing. Criticality is LOW,
MEDIUM, HIGH, CRITICAL or explicit UNKNOWN. The engine does not calculate risk,
derive criticality, certify proof levels or interpret LLM claims about them.

Trusted orchestration must select the authoritative policy version, authenticate
the actor, resolve tenant/repository ownership, enforce applicable permissions and
load the complete relevant evidence set. For graph-backed evidence, use an admitted
repository scope. Evidence has no repository field, so the orchestration layer must
bind its change/run to the selected repository. A caller-provided repository ID or
content hash alone cannot establish this association or scanner authenticity.

Every Evidence value must match the policy tenant and input change/run, have a
unique evidence ID and have `created_at <= AsOf`. Invalid or foreign observations
are rejected even when no rule references them. Evidence must already be sealed
by the existing Evidence model; serialized values pass through `DecodeEvidence`.
The engine never modifies the Evidence schema, original payload or status.

All applicable rules are required. Within each rule, all configured requirements
must pass:

- Known risk above its maximum or proof below its minimum produces FAIL.
- Missing required risk, proof or evidence produces UNKNOWN.
- Every observation matching an evidence requirement's exact producer/type must
  PASS and meet its configured confidence/determinism requirements.
- FAIL or WARN evidence produces FAIL, even at low confidence. A later PASS
  cannot hide adverse evidence. ERROR, SKIPPED, UNAVAILABLE and UNKNOWN evidence
  produce UNKNOWN. A PASS below required confidence or determinism is UNKNOWN.
- A known selector mismatch produces SKIPPED for that rule. An unknown criticality
  needed by a potentially applicable selector produces UNKNOWN, preventing missing
  metadata from bypassing the rule.
- FAIL dominates UNKNOWN; UNKNOWN dominates PASS. All-skipped evaluation is UNKNOWN
  with `NO_APPLICABLE_RULES`, never a vacuous PASS.

Results use `safepatch.policy-result.v1`, include tenant/repository/change/run and
policy ID/version/hash, plus per-rule status and machine-readable reason codes.
Results preserve rule order and sort evidence IDs, so observation ordering does not
change explanations. Errors/cancellation return ERROR without partial rule results.
No payload, credential, untrusted summary or backend error is copied into results.

PASS only means the supplied facts satisfy this configuration. It is not a
production approval, proof of complete evidence collection, or confirmation that
the policy itself was authorized. LLM output must never directly authorize
production; dispatch requires separate trusted authorization and approval controls.

## Entitled policy management

`application.PolicyService.CompileCentral` validates a central policy draft using
the existing FeatureCatalog and ExecutionGuard. Before parsing the document it
resolves the actor's organization scope, requires `policy.central`, checks
`policy.manage`, and invokes the configured quota hook. It then verifies the
document's tenant binding. The quota adapter explicitly allows unmetered validation
or owns applicable reservation/settlement; no numeric policy quota is invented.

This method returns a validated immutable value; it does not publish or activate
it. Local trusted configuration can use the same core New/Decode/Evaluate methods
without cloud management dependencies. No edition-specific evaluator exists.

Revoking central-management entitlement or actor permission prevents new management
operations. Existing required policy evaluation continues. A downgrade must never
disable enforcement, replace a policy, delete its evidence or convert its result
to PASS. Reinstating management access reuses the exact same configuration/schema.
PolicyService has no storage mutation dependency. No existing grants, bundles,
feature identities or frozen v0.1 release matrix are changed.

## Bounds, validation and current scope

Safety bounds are 256 KiB per policy, 128 rules, 64 repository selectors per rule,
16 evidence requirements per rule, 256 observations and 16 MiB aggregate canonical
Evidence per evaluation. JSON nesting reuses the existing 32-level validator.
Evaluation honors context cancellation, uses no filesystem/network/process access
and runs safely with a shared immutable policy. Callers must not concurrently
mutate an Input's caller-owned slices or scalar pointers.

Tests cover the parser, all reference types and evidence statuses, unknown facts,
contradictions, tenant/run isolation, resource bounds, immutable copies, concurrent
evaluation, policy JSON round trips and verifier Record → immutable Evidence →
policy integration. Central-management tests cover entitled and unentitled access,
independent permission denial and unchanged policy/results across grant changes
for all five edition labels. A parser fuzz target is included.

No database schema change is required. Existing migration tests remain in the full
suite; their native PostgreSQL checks require the existing opt-in environment.
Policy persistence, immutable publication history, organization policy assignment,
HTTP/CLI routes, worker dispatch integration, risk/proof computation, freshness
requirements beyond current-run/future-time checks and production approval are
outside this milestone. No hosted policy CRUD service is claimed.
