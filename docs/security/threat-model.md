# SafePatch threat model

Status: design threat model, 2026-09-05. The [audit](../architecture/current-state.md) found no implementation. Every control below is required or proposed and remains unverified; this is not a security certification.

Subsequent scope update: the [repository scaffold](../architecture/repository-structure.md) adds source dependency guards and explicit unavailable-operation exits. Those checks do not implement the runtime security controls in this threat model.

## Assets, actors, and assumptions

Protect tenant source code, credentials, identities, membership, licenses, policy configuration, verification outcomes, proofs, artifact integrity, audit history, and platform availability. Production authorization is a separate protected decision: an LLM must never directly authorize production.

Potential adversaries include malicious contributors, compromised repositories/dependencies/scanners, hostile uploaded artifacts, prompt and MCP injectors, compromised user/service credentials, malicious tenant members, and attackers targeting exposed endpoints or shared infrastructure. Tenant administration does not confer platform-wide access. Self-hosted operators with control of the host are a distinct trust assumption; application controls cannot promise isolation from a hostile host administrator.

## Trust boundaries and intended flow

1. Browser/CLI/service account to Go API or agent/MCP gateway: authenticate, validate input, establish tenant membership, and check permissions and entitlements.
2. API to PostgreSQL and durable work admission: preserve organization scope, atomic state transitions, quota reservations, and idempotency.
3. NATS JetStream to workers: validate envelope provenance, schema, resource ownership, deduplication, and current authority before effects.
4. Worker to repository/scanner/replay sandbox: all repository bytes, build scripts, prompts, scanner responses, MCP output, and uploaded artifacts are untrusted. Do not expose control-plane credentials to executions.
5. Worker to S3-compatible evidence storage and proof evaluation: bind tenant, digest, size, producer, and schema. Validate evidence independently before changing an outcome.
6. Platform to external hosts, billing/license inputs, and observability sinks: constrain egress, authenticate inputs, redact secrets, and scope exported data.

Entitlement checks answer whether an organization owns a capability. Authorization checks answer whether this actor may perform the action. Neither replaces the other or tenant isolation.

## Threats, required controls, and validation

| Threat / entry point | Required control | Required security or error-path test |
| --- | --- | --- |
| Command injection through repository names, paths, prompts, or scanner parameters | Invoke fixed executables with structured argument arrays; prevent option injection; never concatenate untrusted shell commands. Execute repository scripts only inside a constrained sandbox | Shell metacharacters, leading flags, and malicious build scripts cannot execute on the host or access its secrets |
| Path traversal and symlink escape during checkout, extraction, or artifact access | Validate normalized relative paths, absolute/drive/UNC paths, and Windows reparse points; reject escape; use filesystem operations resistant to link races rather than string-prefix checks alone | `..`, mixed separators, absolute paths, symlinks, junctions, and link-swap races cannot escape the approved root |
| SSRF through repository URLs, callbacks, replay targets, or MCP endpoints | Restrict schemes, hosts, ports, and network destinations; block unauthorized loopback/private/link-local/metadata access; revalidate redirects and resolved addresses; enforce egress at the sandbox boundary | Redirect chains, DNS rebinding, IPv4/IPv6 variants, alternate IP encodings, and metadata endpoints are blocked |
| Credential exposure through execution, logs, evidence, or tracing | Least-privilege short-lived credentials, secret separation, redaction, scoped artifact access, and no host credential inheritance | Seeded secrets do not appear in API errors, traces, artifacts, or child-process environments |
| Cross-tenant access through IDs, queries, jobs, caches, graphs, and downloads | Trusted tenant context, scoped repositories, relational ownership constraints, optional defense-in-depth row security, scoped object access, and validated queue bindings | Tenant A cannot read, update, enqueue work for, download from, or join graph data belonging to tenant B using forged references |
| Unsafe archives and decompression bombs | Bound compressed/expanded bytes, entries, depth, and processing time; reject traversal, links, special files, conflicting paths, and unsupported encodings; avoid automatic recursive extraction | Malicious zip/tar fixtures cannot overwrite outside root, create links/devices, or exceed resource budgets |
| Scanner result poisoning or fabricated evidence | Treat scanner responses as typed untrusted input; validate schema, bounds, run binding, tool identity/version, artifact digest, and provenance before policy evaluation | Forged run/tenant IDs, malformed results, duplicate/conflicting outcomes, missing evidence, and invalid digests never produce PASS |
| Resource exhaustion and quota races | Admission limits plus sandbox CPU/memory/disk/network/time/process caps; bounded parsers/queues; atomic tenant quotas and concurrency reservations | Concurrent requests cannot exceed configured reservations; cancellation, timeout, crash, and retry release or reconcile resources correctly |
| Prompt/MCP injection and confused-deputy actions | Keep text as data; validate tool requests against schemas, allowlists, tenant scope, permissions, entitlements, and policy; constrain agent leases | Instructions embedded in source, evidence, or tool output cannot grant capabilities, expose secrets, or authorize production |
| Subscription/permission confusion and stale license grants | Separate entitlement and permission decisions, signed tenant-bound licenses, explicit expiry handling, revisioned caches, and audited changes | Entitled but unauthorized actors are denied; expired/forged licenses and stale membership do not retain privileged effects |
| Work replay, duplicate delivery, and admission/worker races | Idempotency keys, transactional work publication, explicit state transitions, usage reconciliation, and rechecks before sensitive effects | Duplicate/out-of-order deliveries and downgrade/revocation between enqueue and execution cannot double-execute or bypass access checks |
| Evidence tampering, insecure exports, and mutable attestations | Immutable artifacts with verified digests, authenticated provenance, tenant-bound reads, scoped expiring download authority, and audited issuance | Replaced bytes, altered metadata, wrong-tenant downloads, and expired access fail validation |
| Unsafe runtime replay/shadow effects | Default to isolated destinations and scoped credentials; require explicit trusted policy for side effects; constrain network and execution budgets | Recorded calls cannot silently reach production payment, email, infrastructure, or private endpoints |
| Outcome confusion or dependency outage | Model PASS, FAIL, and UNKNOWN explicitly; missing, failed, timed-out, or inconclusive checks remain UNKNOWN or FAIL under defined policy; fail closed at authorization | Scanner outage, parser failure, missing evidence, and permission/entitlement lookup errors cannot yield PASS or permit production |

## Deployment and operational requirements

Use least-privilege database, queue, and object-store credentials with authenticated connections. Restrict administrative interfaces, enforce dashboard session protections and CSRF defenses where applicable, and audit security-sensitive configuration and grant changes. Public error messages must not leak resource existence across tenants.

Sandbox workers must not share privileged host sockets or control-plane credentials. Redact sensitive attributes before sending OpenTelemetry data. Backup, restore, retention, and export procedures must preserve tenant ownership and evidence integrity. Validate a restored database/object set together; a successful database restore alone does not prove proof completeness.

Self-hosted and air-gapped configurations retain the same checks. Specify offline license validation, update distribution, clock assumptions, revocation latency, and trust roots before implementation. A channel or edition label never relaxes core security requirements.

## Residual risks and open decisions

- No control is currently implemented or tested in this workspace.
- Execution isolation technology, scanner trust roots, permission granularity, policy authority, signing-key management, and incident response procedures remain undefined.
- The production approval workflow must define which trusted humans/services may authorize an action and which deterministic policy gates apply. LLM recommendations remain untrusted supporting data.
- Offline deployments cannot assume immediate online revocation. Grace periods and revocation behavior need explicit product/security decisions.
- No runtime, infrastructure, migration, dependency, or tenant-isolation assessment is possible until those components exist. Track control implementation and negative-test evidence before making readiness claims.

Required validation includes unit, integration, security, error-path, migration, and entitlement-boundary tests. Follow [shared-platform acceptance requirements](../architecture/one-codebase-editions.md#required-acceptance-tests), including all four premium access/data-preservation cases.
