# Entitlement limits and quotas

The shared Go domain defines `EntitlementLimit` in `internal/domain/entitlement/limit.go`. A limit is an effective organization resource policy, independent of feature ownership, actor permissions, edition, product version and release channel. All five editions use the same service, storage adapters and migration history. This milestone implements accounting infrastructure, not a verifier or a premium execution engine.

| State | Meaning |
| --- | --- |
| `UNLIMITED` | No commercial ceiling; accounting still uses checked int64 arithmetic. Value must be zero. |
| `INTEGER` | Nonnegative explicit ceiling. Zero permits no positive consumption. |
| `DISABLED` | No new admission. Value must be zero. |

Unknown keys/states, invalid values, missing policies, storage errors and invalid clocks fail closed. No numeric plan defaults have been invented. Existing nullable feature `LimitValue` fields and `EntitlementService.Limit(feature.Key)` retain their existing finite-or-unspecified semantics; null never becomes unlimited. Resource policies are separate typed values and do not grant features.

| Stable resource key | Accounting and units |
| --- | --- |
| `repositories` | Reusable capacity, repository count |
| `developers` | Reusable capacity, developer count |
| `proof_credits_monthly` | Consumption per UTC calendar month, credits |
| `verification_concurrency` | Reusable capacity, active verification slots |
| `retention_days` | Per-request ceiling, days; never summed |
| `artifact_storage_bytes` | Reusable capacity, bytes |
| `replay_requests_monthly` | Consumption per UTC calendar month, requests |
| `fuzz_seconds_monthly` | Consumption per UTC calendar month, seconds |

## Service contract

```go
quota, err := entitlement.NewQuotaService(store, time.Now)
err = quota.Check(ctx, organizationID, key, amount)
reservation, err := quota.Reserve(ctx, organizationID, key, amount, operationID)
reservation, err = quota.Commit(ctx, organizationID, operationID)
reservation, err = quota.Release(ctx, organizationID, operationID)
```

Check is advisory for additive resources. Reserve is the atomic admission decision and must precede work. Requested amounts must be positive and calculated by trusted operation policy. Reserve rejects `retention_days`; Check compares the requested retention duration directly against its ceiling.

Reserve increments held usage; Commit moves the full hold to consumed usage without changing the total. Release cancels uncommitted holds. For reusable resources, Release also frees committed capacity after the repository/member/artifact is removed or verification stops. Committed monthly consumption cannot be refunded. Concurrency remains held through Commit until Release. Partial settlement is not supported; callers must reserve the amount they intend to commit or define smaller independently identified work units.

Operation IDs are bounded opaque strings, unique within an organization across all resources and periods. Reserve retries with identical arguments return the original RESERVED or COMMITTED record. Different arguments or a released ID produce a conflict. Repeated Commit/Release operations are idempotent in their corresponding terminal states. A reservation is accounting evidence, not an execution lease: an idempotent Reserve response must never cause duplicate business execution. Workflow dispatch needs its own durable deduplication/outbox and ownership semantics.

Monthly periods are computed by the injected trusted clock after storage locking, using UTC calendar months. A reservation keeps its original period; settlement and retries after midnight/month rollover do not charge the new period. Outstanding work may therefore consume its original month's allowance later. No destructive counter reset is needed.

New admissions read the current policy under the same lock as policy updates. Reducing or disabling a limit does not delete usage, reservations, repositories or artifacts. Over-limit organizations cannot reserve more. Existing holds can still settle or be released after downgrade/revocation because settlement records work already admitted; this does not authorize further work after ownership or permission is revoked.

## Storage and composition

`QuotaStore.WithinOrganization` provides one serialized transaction for current limit reads, usage and reservation changes. Cancellation, callback errors and panics roll back all writes. No successful reservation is returned on storage failure. A database commit failure may have an ambiguous outcome; retry using the same operation ID, never a fresh ID.

`internal/platform/quotastore.Postgres` uses an injected `database/sql` connection pool, parameterized tenant predicates and a per-organization row lock shared by admission, settlement and SetLimit. Locks serialize different workers/processes and concurrent first use. Read-committed isolation makes policy updates visible after lock acquisition. This intentionally favors simple correctness over high concurrency within one organization. Use a primary connection pool with a fixed trusted search path, deadlines, TLS and driver registration supplied by deployment composition. No driver dependency or database bootstrap was added to command shells.

Migration `000003_entitlement_limits_quotas.up.sql` adds resource identities, organization accounting locks, effective policies, counters and reservation history. Composite keys/FKs bind reservations to their tenant's bucket. Constraints reject invalid states, periods, amounts, counter overflow and forbidden reservation transitions. Existing edition migrations and bundles are unchanged. No organization registry exists yet, so quota_accounts is not proof of an organization's existence or membership. Provisioning must establish those through trusted organization storage before calling the adapter. SQL table access belongs only to trusted service roles; direct arbitrary SQL can bypass the service's accounting invariants. The adapter supplies scoped queries, not a complete PostgreSQL RLS deployment.

SetLimit is an adapter-level trusted provisioning method. A future policy ingestion adapter must resolve billing/license/trial/configuration policy, authorize the change, and publish effective resource limits there. Current feature resolver/snapshot caches are unchanged and do not resolve these resource policies. SetLimit neither edits historical edition bundles nor resets quota history. Changes to multiple policies and feature grants are not yet one shared provisioning transaction.

`quotastore.Memory` is bounded and transactionally rolls back copies; it is for tests or process-local embedded use. Multiple service objects can share it, but separate processes cannot. Restarting it loses all accounting. Configured organization/reservation bounds fail closed rather than evicting idempotency records.

The existing `application.GuardedOperation` remains the enforcement order: authenticate, resolve tenant/resource ownership, `EntitlementService.Has`, `PermissionService.Allowed`, quota admission, operation. Its `QuotaChecker.Check` hook must call Reserve for additive work, then carry the ID into settlement. Wiring the advisory QuotaService.Check alone would allow overbooking. Integration tests exercise this path with actual seeded editions, ownership resolution, permissions and quota accounting. Worker settlement is a trusted internal action scoped by organization and durable operation identity, not an unrestricted user endpoint.

## Validation and remaining work

Unit, integration, security and error-path tests cover all keys/states, capacity reuse, calendar rollover, downgrade/upgrade preservation, tenant isolation, permission denial after entitlement, overbooking, duplicate retries, overflow, poisoned storage, cancellation, rollback and bounded memory. Scripted database/sql tests check scoped query arguments, lock/isolation setup, policy updates and transaction failures. Native PostgreSQL migration assertions additionally check constraints, immutable identities, rollback and preserving edition history; enable them as documented in [migrations](../../migrations/README.md). SQL transport tests do not prove live PostgreSQL behavior.

No automatic hold expiry/reaper is implemented. Lost workers leave holds until trusted reconciliation calls Release; automatic expiry would risk freeing capacity while work still runs. Reservation identity/history is retained indefinitely; durable archival/tombstone policy, reconciliation, operational metrics, rate limiting and database storage budgets remain deployment work. Resource quotas do not replace scanner timeouts, archive limits or other independent security bounds. No API endpoint, verification dispatch, NATS work, artifact deletion, production approval or LLM authorization was added.
