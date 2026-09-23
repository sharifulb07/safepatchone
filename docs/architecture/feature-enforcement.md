# Reusable feature enforcement

SafePatch uses one ownership check across CLI preflight, API admission and worker dispatch. `entitlement.RequireEntitlement(ctx, service, organizationID, featureKey)` validates input and calls `EntitlementService.Has` on every invocation. A negative answer returns `FeatureDenied`, wrapping `ErrNotEntitled`. Errors, nil services, invalid input and cancellation cannot become an allow, including an adapter returning `true` together with an error. The helper retains no cache and never consumes client-provided grant flags.

Entitlement service dependencies are trusted composition inputs. Server/worker composition must supply the authoritative organization resolver or revision-checked `SnapshotService` with a current primary source, never a client snapshot or stale-on-error cache. The offline Community provider is local CLI policy, not a fallback for cloud failures. Its local namespace cannot establish ownership of a remote tenant.

## Shared access sequence

`ExecutionGuard` extracts and reuses the existing guard's tenant, entitlement, permission and quota sequence. `GuardedOperation` still authenticates first and preserves its public constructor and Execute contract. API middleware uses its new `Authorize` method to finish admission before writing any HTTP response. Worker dispatch supplies the authenticated actor recorded in trusted durable work storage to `ExecutionGuard`, then re-resolves current scope and permissions.

The order remains:

1. Authenticate the API request, or load authenticated job provenance from trusted worker storage.
2. Resolve current actor membership and resource ownership within the requested tenant.
3. Require the route/workload capability using current entitlement state.
4. Independently check `PermissionService.Allowed` for the actor and bound permission.
5. Admit quota (reserve additive resources atomically, without waiting for future capacity).
6. Invoke the handler or workload.

Every failed stage stops subsequent stages. Transport errors suppress private backend details; internal callers retain `errors.Is`/`errors.As`. `application.ErrEntitlementDenied` aliases the shared `entitlement.ErrNotEntitled` sentinel. Capability names are configuration at trusted route/workload composition, never edition-name branches or request fields. Neither feature ownership nor actor permission authorizes production without the required policy and approval controls.

## API middleware

`internal/platform/httpapi.NewEntitlementMiddleware` requires an authenticator, tenant resolver, entitlement service, permission checker, quota checker, bound permission and HTTP request reader. The request reader extracts credentials and requested scope; it does not establish authentication or tenant ownership. All dependencies are mandatory. For a replay route, bind `authorization.KeyReplayExecute` in its `EnforcementConfig`, then:

```go
wrap, err := middleware.RequireEntitlement("runtime.replay")
if err != nil {
    return err // fail route composition
}
handler := wrap(replayHandler)
```

The application guard rechecks access even for a direct HTTP client that bypasses every UI and CLI. User-supplied `feature`, `entitled`, `plan`, `permission` and `approved` claims are not inputs to the middleware's capability/permission selection. `ExecutionFromContext` exposes validated actor, resource scope and requirement to the downstream handler, but its output is not a durable worker credential. Missing context must never be treated as authorization.

Rejected requests receive JSON `{"error":{"code":"...","message":"...","feature":"runtime.replay"}}`, with `Cache-Control: no-store` and `X-Content-Type-Options: nosniff`:

| HTTP status | Code |
| --- | --- |
| 400 | `INVALID_REQUEST` |
| 401 | `AUTHENTICATION_REQUIRED` |
| 403 | `FEATURE_NOT_ENTITLED`, `PERMISSION_DENIED`, `TENANT_ACCESS_DENIED` |
| 429 | `QUOTA_DENIED` |
| 503 | `ENFORCEMENT_UNAVAILABLE` |

Infrastructure failure or cancellation does not invoke the handler. Once the handler starts, it owns its response and operation error handling; the middleware does not append a second JSON failure to a partially written response. Route composition must install the wrapper; merely defining it does not protect an unwrapped route. No production HTTP endpoint or server listener was added in this milestone.

## CLI helper

```go
err := application.RequireFeature(ctx, entitlements, organizationID, "runtime.replay")
if err != nil {
    return err
}
```

The helper returns the existing structured `CLIError`, including `FEATURE_NOT_ENTITLED`, `UNKNOWN_FEATURE`, `INVALID_USAGE` and `ENTITLEMENT_UNAVAILABLE`. Causes remain private in JSON, and messages contain no sales pitches. `safepatch verify` now uses the helper for `verify.basic` before its existing explicit `NOT_IMPLEMENTED` response. This exercises the shared gate without pretending the verification engine exists. Entitlement explain remains metadata inspection rather than an execution path.

A successful CLI check is preflight only. A cloud API must repeat enforcement regardless of local checks, and a worker must repeat it at dispatch. Do not serialize a CLI success boolean into a trusted job grant.

## Worker dispatch

`NewWorkerOperation` binds a workload kind, capability, actor permission and operation callback at worker composition. Its dependencies include a `WorkloadRepository` and the same tenant/entitlement/permission/quota services. A replay worker binds kind `replay`, `runtime.replay`, and `replay.execute`.

```go
err := replayWorker.Start(ctx, organizationID, workloadID)
```

Queue input supplies only organization/workload identifiers. `Start` reads the current tenant-scoped durable work record, checks returned ID/organization/kind and actor/resource structure, then performs current access checks immediately before invoking the expensive operation. It does so on every dispatch and retry. The stored record must originate from authenticated enqueue handling; syntax alone cannot authenticate the stored actor. The repository returns `ErrWorkloadNotFound` for missing or other-tenant records, and errors never become an empty valid job.

No role, feature key, entitlement snapshot or prior approval flag is accepted from queue data. A wrong workload kind cannot select a cheaper entitlement gate. Revocation, trial expiry or loss of actor permission after API/CLI admission prevents the worker callback. Restoring entitlements can permit a subsequent dispatch without changing queued data or old grant records.

This helper is not a NATS consumer, scheduler, job-state transition system, deduplication lease or worker persistence adapter. Retrying Start can invoke work again if the surrounding dispatcher has not already claimed/completed the job. Durable job claims, acknowledgement/retry policy and quota settlement belong to that dispatcher. No credentials need to be copied into the queue payload.

## Limits and validation

The check is admission-time validation, not a transaction spanning subscription state and an arbitrary side effect. A revocation concurrent with starting work does not automatically cancel already running work. Long-running execution needs cancellation/lease policy and rechecks at later privileged boundaries; waiting jobs must call Start when dispatching, not when first received. Quota adapters should reserve promptly or deny, not sleep waiting for a slot after the entitlement check. They and the workflow own Commit/Release and cleanup if execution fails. No entitlement, permission or quota bypass should be added to accommodate planned engines.

Tests cover the shared gate, exact stage ordering, structured CLI failure, direct HTTP access despite forged feature claims, independent permission denial, cross-tenant/wrong-kind stored jobs, current-state checks on every dispatch, cancellation, backend errors, HTTP concurrency and error sanitization. Integration tests use actual migration bundles for all five editions, the real snapshot resolver and PermissionService across CLI/API/worker boundaries. They revoke and restore subscriptions, revoke actor membership, expire trials after CLI admission, and prove existing workload and grant data remain unchanged.

No database schema, migration history, edition allocation, release channel or frontend behavior changes are required. Native PostgreSQL tests remain opt-in; the new integration tests exercise application contracts with controlled storage/identity fixtures. Production identity, membership/snapshot persistence, job storage and route/worker composition remain to be implemented before deployment.
