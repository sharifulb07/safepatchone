# EvidenceQuorum

`internal/proof/quorum` implements one deterministic evaluator for independent
verification families. `QuorumPolicy` and `QuorumResult` are sealed canonical values;
`QuorumRequirement` describes a named proof obligation. The stable FeatureCatalog
identity is `feat_0046` / `proof.quorum` (migration `000021`). Registration does not
change existing immutable edition bundles or grant access.

Trusted composition assigns each exact producer/type to one family: `tests`,
`property_testing`, `static_analysis`, `replay`, `business_invariant`, `formal`, or
`ai_review`. Unit test runners share `tests`; two static scanners share
`static_analysis`. Repeated observations and scanner aliases count once per family.
`IndependenceID` identifies the underlying method/engine across wrappers. Assigning
one identity or technical capability to different families is rejected. These
assignments require an audited source catalog; scanner JSON and content hashes
cannot establish independence or provenance. A multi-method tool needs separately
audited method identities and capabilities.

Each requirement selects relevant source IDs, a positive family count, optional
mandatory families, a confidence floor, and an optional determinism requirement.
For payment correctness, configure unit tests, property testing, Replay and a
business invariant with a count of four to require all four. A count of three can
be satisfied by tests, Replay and BusinessGuard. Named mandatory families must pass
even if other sources already meet the count. Producers/types should identify the
specific property check; generic test success does not establish payment coverage.

The loader supplies the complete relevant evidence set for one tenant, repository,
immutable change and run. Missing collection completeness prevents satisfaction.
Unknown/unavailable/skipped/error observations cannot contribute a passing family;
an incomplete matching observation prevents that family from counting even if
another observation passes. An absent alternative scanner does not invalidate a
family established by another qualified scanner in that family.

Every relevant FAIL or WARN vetoes the entire requirement, regardless of confidence,
determinism or how many other families pass. In particular, tests PASS + static
analysis PASS + Replay FAIL is `NOT_PROVEN`; positive AI reviews cannot reverse it.
Explicit EvidenceGraph `CONTRADICTS` or `INVALIDATES` edges touching relevant
observations also veto satisfaction. Positive graph edges never upgrade evidence.
All observations and graph endpoints must belong to the same tenant/change/run;
duplicate identities, foreign observations, future timestamps and dangling edges
are rejected. Errors return no usable result.

Results show each satisfied family, missing mandatory families (or eligible
remaining families when the count is short), the numeric deficit, and exact
evidence IDs/hashes, source IDs, families, statuses and reasons. Contradiction
explanations include graph edge IDs and both endpoint evidence identities. Input
ordering does not change canonical results. A satisfied result emits canonical
Evidence PASS; contradiction emits FAIL; insufficient proof emits UNKNOWN.
The public quorum decision for both failure cases remains `NOT_PROVEN`.

## Integration

- `application.EvidenceQuorumService` resolves the repository tenant, requires
  `EntitlementService` access to `proof.quorum`, checks `PermissionService`
  `proof.execute`, then invokes the existing quota hook before loading evidence.
  It rechecks current access before returning. The injected QuotaService adapter
  owns any reservation and settlement; pure bounded aggregation can be explicitly
  unmetered. No numeric commercial quota is invented. Each underlying verifier
  retains its separate feature, permission and execution quota admission.
- `proof.NewQuorumProofPlanner` extends the existing planner with all eligible
  technical capabilities at a configured proof level. This deliberately schedules
  all candidate sources, including contradiction detectors; it does not optimize
  the smallest k-of-n subset. Missing access/readiness/budget keeps that plan
  BLOCKED. The quorum policy digest is included in planner identity. READY is
  scheduling information and never satisfies a proof requirement.
- `policy.Requirements.Quorum` embeds the validated policy configuration. The
  PolicyEngine evaluates it from raw trusted evidence, with `QuorumComplete`
  supplied by the loader, and returns the full explanation. Required evaluation
  has no commerce dependency and continues after grant revocation. PolicyGuard
  flags removal or any change to a baseline quorum for review.
- `trust.QuorumCoverage` maps satisfied requirements to exact evidence IDs. Missing
  or contradicted proof cannot claim complete coverage. TrustKernel re-evaluates
  the authoritative policy, consumes graph contradictions and preserves its
  existing intent, freshness, provenance and decision precedence checks. Quorum
  results never directly authorize an operation or raise an achieved proof level.
  Its `KernelInput.QuorumComplete` is the loader's collection-completeness fact,
  independent of `ProofCoverage.Complete`, which describes proof satisfaction.
- `QuorumResult.Evidence(id, intentDigest)` emits the existing canonical Evidence
  envelope. `Relationships` produces explanation edges. `Store` validates retained
  source hashes, then writes the result and edges through `graph.EvidenceGraph`
  using a current admitted single-repository graph write scope. Source nodes must
  already exist. Exact retries are idempotent; partial writes return errors and
  must be retried. GraphAccess and retention checks remain mandatory at composition.

Self-hosted deployments inject the same entitlement, permission, quota, source and
graph contracts, including an offline license provider where applicable. No hosted
service or edition-specific execution path is required. Community registration
does not grant Replay, advanced quorum operations, cloud storage or production
authority. Existing required security policy continues to enforce quorum rules.

This change supplies the core and application service contracts; host composition
must supply an authoritative `QuorumSource` and existing graph admission. It does
not add a CLI command, HTTP endpoint, scanner executor or proof-collection daemon.

## Validation

Tests cover all evidence statuses, thresholds, mandatory families, scanner aliases,
contradictions despite AI success, incomplete collection, malformed configuration,
scope isolation, canonical explanations, graph retry failures, planner budgets,
policy parsing/removal, TrustKernel decisions and forged coverage. Admission tests
exercise all five edition labels, grant revocation/restoration, independent actor
permissions, quota rejection and revocation during loading. Existing Community
bundle grants remain pinned. The native migration test uses the repository's
opt-in PostgreSQL harness; a skipped native test is not SQL execution evidence.
