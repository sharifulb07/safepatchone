# EvidenceGraph

`internal/graph.EvidenceGraph` defines one directed evidence graph for Community,
Developer, Team, Business and Enterprise. `internal/platform/graphstore.Postgres`
is its PostgreSQL implementation. It stores the existing sealed Evidence schema;
no edition fields, alternate engines or capability-specific node types are added.

## Relationships

For an edge from A to B:

| Relationship | Assertion |
| --- | --- |
| `SUPPORTS` | A provides support for B |
| `CONTRADICTS` | A conflicts with B |
| `DERIVED_FROM` | A was derived from B |
| `VALIDATES` | A records validation of B |
| `INVALIDATES` | A records invalidation of B |
| `DEPENDS_ON` | A depends on B |

These are explicit assertions recorded by trusted orchestration. There is no
automatic reverse edge, transitive inference, verdict aggregation or production
authorization. Cycles and distinct contradictory assertions are legal. Self
edges and unknown relationship keys are rejected. In particular, a `VALIDATES`
edge never changes an UNKNOWN Evidence status to PASS.

## Persistence and operations

Apply `000005_evidence_graph.up.sql` after the four existing migrations. It adds
`evidence_graph_nodes` and `evidence_graph_edges`, indexes, composite tenant foreign
keys and append-only triggers. It preserves all earlier edition and quota data.

The adapter accepts a configured `*sql.DB`, following the existing quota adapter.
Composition owns driver registration, a primary PostgreSQL connection, TLS,
credentials, a fixed trusted search path and request deadlines. Database accounts
belong to trusted services; scanner processes must never receive these credentials.
Use a migration owner separate from application roles, granting application roles
only SELECT/INSERT on graph tables. The schema does not implement row-level
security or defend against an administrator disabling triggers.

- `PutEvidence(ctx, scope, repositoryID, sealed)` stores canonical Evidence text
  verbatim, its hash and a repository binding. `(tenant_id, evidence_id)` is unique.
  Exact retries succeed; changing bytes or the repository for an ID conflicts.
- `GetEvidence(ctx, scope, evidenceID)` verifies the canonical envelope and hash on
  read. Missing, inaccessible and expired records return `graph.ErrNotFound`.
- `AddRelationship(ctx, scope, edge)` checks both endpoints within the authorized
  tenant, repository set and time window. Edge IDs and directed relationship triples
  are unique within a tenant. Exact retries succeed; replacement data conflicts.
- `Neighbors(ctx, scope, query)` returns one bounded adjacency page, with incoming,
  outgoing or both directions and an optional relationship filter. It checks the
  anchor and both endpoints; inaccessible endpoints produce no visible edge.

Writes use transactions and never perform UPDATE-on-conflict. PostgreSQL prevents
node/edge deletion, updates and truncation. Corrections require a new Evidence ID
or a new explicit relationship, preserving the old assertions. Same evidence/edge
IDs in different tenants remain independent. Tenant identity and repository lists
are bound SQL parameters, including every adjacency join.

The original Evidence envelope retains all nanoseconds and canonical bytes. Graph
time indexes, edge timestamps and scope bounds use UTC microseconds, truncating
fractional nanoseconds consistently. The migration checks that indexed node time
matches that projection. Time windows are inclusive. This precision convention
never changes an Evidence hash or stored status.

Queries accept at most 64 explicitly authorized repositories and 100 edges per
page. These are resource safety bounds, not edition quotas. `AfterEdgeID` is an
exclusive cursor sorted with PostgreSQL's C collation; `NextCursor` is set when
another row exists. It is neither an authorization credential nor a complete
snapshot token. Concurrent inserts can affect subsequent pages. Callers must
re-authorize each page and set context deadlines for database work.

## Entitlement and authorization

`application.GraphAccess.Authorize` produces a scope after this sequence:

1. Validate an authenticated user/service principal and every requested repository.
2. Resolve ownership of each repository in the target tenant.
3. Check current feature grants with `EntitlementService.Has`.
4. Check `proof.view` for reads or `proof.execute` for writes with PermissionService.
5. Read the current `retention_days` policy through the existing quota store.

| View | Required grants |
| --- | --- |
| Single repository (`LOCAL`) | `graph.local` |
| Organization, one repository | `graph.local`, `graph.organization` |
| Organization, multiple repositories | Above plus `graph.cross_repo` |
| Explicit cross-repository view | `graph.local`, `graph.organization`, `graph.cross_repo` |

The view is selected by trusted operation policy. `LOCAL` describes the repository
scope; it does not simulate a hosted organization graph on an offline CLI.
An organization view contains only the explicitly resolved repository set, not an
unbounded implicit organization scan.

`UNLIMITED` retention exposes all stored history up to the current time. Positive
`INTEGER` values restrict the view to that many UTC calendar days. Zero, DISABLED,
missing/invalid policies, lookup errors and denied permissions fail closed.
Arbitrarily large valid integer policies cannot overflow time calculations.
Downgrades narrow future scopes without deleting nodes or edges; restoring a grant
or retention period makes preserved history eligible for access again.

An admitted `graph.Scope` is internal, short-lived input, never accepted directly
from a request or stored job. Call Authorize before each operation and again at
worker start. Ownership of new evidence's change/run/repository must come from
trusted orchestration, not scanner payloads. Artifact storage quota reservations
and artifact authorization remain separate existing admission responsibilities.

```go
scope, err := access.Authorize(ctx, actor, organizationID,
    []string{repositoryID}, application.GraphLocal, false)
if err != nil { return err }
page, err := store.Neighbors(ctx, scope, graph.Query{
    EvidenceID: evidenceID, Direction: graph.Both, Limit: 50,
})
// Handle err before using page. Never expose raw database errors or payloads.
```

No grant, edition bundle, numeric plan default or frozen v0.1 feature matrix is
changed. No HTTP/CLI graph route, hosted service composition, recursive traversal,
automatic evidence ingestion, retention purge, archival tier or graph visualization
is introduced by this milestone. Retention here controls access, not a physical
deletion SLA. Large artifact bytes continue to use existing immutable artifact
storage; graph operations never fetch URLs, execute code or extract archives.

## Validation

Core and admission tests cover relationships, bounded requests, tenant resolution,
each grant, independent permissions, current retention, downgrade/restore and
cancellation. Scripted `database/sql` transport tests cover exact tenant bindings,
transactions, idempotency conflicts, original byte preservation, corrupt data,
endpoint filtering, pagination and error rollback. These tests do not emulate
PostgreSQL SQL execution or concurrency semantics.

`TestPostgresEvidenceGraphMigration` applies the entire migration history and SQL
assertions for all six relationships, cycles, isolation constraints, immutability,
nanosecond projection, unchanged prior history, plan changes and reapplication.
It uses the existing opt-in PostgreSQL test harness:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
# Configure libpq connection settings for a disposable test database.
go test ./internal/platform/migrationtest -run TestPostgres -count=1 -v
```

Without PostgreSQL/psql and explicit opt-in, native tests skip. Run them in a
provisioned environment before deploying this migration. Always run
`go test ./...` and `go test -race ./...` for the shared core.
