# PermissionService and guarded request flow

`internal/authorization.PermissionService` answers whether an authenticated actor has an organization-wide permission. `internal/entitlement.EntitlementService` answers whether the organization owns a capability. Their inputs, repositories, and decisions remain independent. Both must pass before quota admission and execution.

## Permissions and initial roles

| Permission | Owner | Viewer |
| --- | --- | --- |
| `proof.execute` | Allow | Deny |
| `proof.view` | Allow | Allow |
| `policy.view` | Allow | Allow |
| `policy.manage` | Allow | Deny |
| `replay.execute` | Allow | Deny |
| `replay.view` | Allow | Allow |
| `agent.approve` | Allow | Deny |
| `agent.manage` | Allow | Deny |
| `audit.view` | Allow | Allow |
| `billing.manage` | Allow | Deny |
| `organization.manage` | Allow | Deny |

Owner and viewer are membership roles, not commercial editions. These are explicit finite grants; wildcard and unknown permission keys fail closed. The initial viewer policy permits the four read permissions above, including audit viewing. Custom roles, resource-specific ACLs, role delegation, and additional built-in roles are outside this milestone.

```go
permissions, err := authorization.NewPermissionService(membershipRepository)
// Allowed(ctx, authenticatedPrincipal, organizationID, permissionKey) (bool, error)
```

Principal contains an opaque actor ID and kind (`user` or `service_account`). Authentication must establish it. Membership contains organization ID, that exact principal, role, and active state. Repository reads must scope by organization, actor kind, and actor ID so user and service-account identities cannot collide. Missing or inactive membership denies. Invalid records, scope mismatches, unknown roles/keys, repository failures, and cancellation return false. Roles are reread on every call; no permission cache retains removed members or old roles.

Service accounts require explicit current memberships; they gain nothing merely by being authenticated. This permission model does not designate a human approval or validate an agent's production decision. An `agent.approve` grant is one permission gate, never sufficient evidence that an LLM may authorize production. Production dispatch needs separate trusted policy and approval enforcement.

## Request flow

`internal/application.GuardedOperation` executes:

```text
authenticate -> tenant resolve -> entitlement check -> permission check -> quota check -> operation
```

Construct it with Authenticator, TenantResolver, EntitlementService, PermissionChecker, QuotaChecker, a fixed Requirement, and an operation callback. All dependencies are required. Requirement binds the feature key and permission key at trusted composition time. For example, a proof-planning execution requires `proof.planner` plus `proof.execute`; its read operation requires `proof.planner` plus `proof.view`.

Request contains only a credential and requested tenant/resource scope. It cannot supply roles, an authenticated principal, or weaker feature/permission requirements. Basic size/identity validation and cancellation precede authentication. The guard then validates the authenticated principal, requires the resolved organization/resource to match the requested target, calls EntitlementService.Has, calls PermissionService.Allowed, checks quota, and executes. Each failed or denied gate stops all subsequent gates. A true value accompanied by an error is still failure.

TenantResolver must prove current membership and resource ownership from trusted storage. Syntax validation or an ID supplied by the client is not proof of tenant ownership. The permission service rereads membership after entitlement passes. Resource-specific restrictions beyond organization membership must be added at a separate policy/ACL boundary when requested; they are not implied by this organization-wide role model.

QuotaChecker must atomically enforce or reserve applicable usage, or explicitly admit an unmetered operation. This milestone defines the required check and ordering; it does not implement a quota ledger, reservation finalization/release, or accounting policy. Failure or cancellation after a reservation needs an adapter-defined lifecycle. Operation side effects also require their own idempotency policy; an error or cancellation does not imply rollback.

Credentials are supplied only to the authentication adapter. Later stages receive the authenticated principal and the resolved scope. StageError exposes a stable stage message while retaining the cause for internal errors.Is/As handling. Transports must not expose unwrapped adapter errors or log raw credentials.

## Edition independence and evidence

The integration test uses the actual versioned edition seed data, canonical entitlements, SnapshotService/EntitlementResolver, PermissionService, and GuardedOperation. Identity, tenant storage, memberships, quotas, and the operation itself are controlled fixtures.

For a Team organization owning `proof.planner`, its owner can execute and its viewer cannot; both can view. The entitlement snapshot content hash remains identical across those actor checks. The test also runs all five seed bundles, verifies an owner cannot bypass a missing capability, simulates a subscription change, preserves historical grants, and proves independent actor permissions survive a plan change while execution is blocked by the missing entitlement.

Unit/security/error tests cover all eleven permissions, tenant/actor/kind mismatches, disabled/missing members, unknown keys/roles, revocation rereads, concurrency, malformed identities, exact gate order, denial/error/cancellation at every stage, and credential-safe error messages. Architecture tests prohibit authorization from importing entitlement services, entitlement domain records, edition metadata, billing, or infrastructure.

There are no edition-dependent role grants, migrations, seed changes, or new premium feature implementations. Software Version != Commercial Edition != Release Channel.

## Remaining boundaries

Authentication, tenant resolution, membership persistence, quotas, production approval, and live operation adapters are not implemented. Command shells still report operational behavior unavailable; no HTTP route, worker, or verification engine is enabled by this milestone. Earlier entitlement tests using a fake permission checker remain historical resolver tests; the new integration uses the real PermissionService.

Checks are current at their respective reads, not one distributed transaction. A revocation can race with later execution. Queued or long-running work must reauthenticate/revalidate at admission or use a separately designed execution lease; never treat a previous permission result as permanent authorization.

Next recommended prompt: implement tenant-scoped membership persistence and trusted authentication/tenant-resolution adapters, with cross-tenant and revocation integration tests, while preserving the guarded request order.
