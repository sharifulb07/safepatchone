# Revision-aware Causal ChangeGraph

`internal/graph.ChangeGraph` adds revision-pinned code and system entities to the
existing shared graph architecture. The existing `graphstore.Postgres` implements
both EvidenceGraph and ChangeGraph. Every edition uses these same models, queries,
tables and migration history. This milestone supplies the core, PostgreSQL adapter
and guarded application service; it does not enable a new hosted product workflow.

## Identity and history

An entity has `(tenant_id, repository_id, revision, entity_id)` identity. A stable
entity ID may recur at different revisions without replacing the earlier node.
Collectors must supply immutable source revision identities, such as commit IDs
or immutable collection IDs for runtime observations. The graph does not resolve
mutable branch names, discover repositories or infer entity identity across renames.

Every request supplies a `RevisionPin` for each explicitly authorized repository.
`RevisionScope` rejects missing, duplicate, foreign or ambiguous pins. A view may
select different revisions for different repositories, but exactly one revision
for each repository. It never chooses an implicit latest revision. Read separate
pinned views to compare historical revisions; absence alone does not prove deletion
or complete collection.

The immutable `ChangeNode` contains schema version, tenant, entity reference, kind,
name, optional repository-relative path, source SHA-256 input hash, bounded JSON
payload and creation time. Files require a path. Ten supported kinds are:

| Kind | Represents |
| --- | --- |
| `file` | Source/configuration file |
| `symbol` | Function, method, class or type |
| `api` | API endpoint or interface |
| `db` | Database object or operation |
| `event` | Event contract or topic |
| `service` | Service boundary |
| `test` | Test or test suite |
| `dependency` | Package or external dependency |
| `runtime` | Collected runtime relationship/observation |
| `incident` | Incident associated with a pinned source revision |

`ChangeLink` contains schema version, tenant, immutable tenant-unique link ID,
revision-qualified endpoints, relation, causal basis, evidence ID and creation time.
Relations are directed assertions: `CONTAINS`, `CALLS`, `READS`, `WRITES`,
`PUBLISHES`, `CONSUMES`, `DEPENDS_ON`, `TESTS`, `RUNTIME_CALLS`, `CAUSES`.
Cycles and independently evidenced assertions are legal. Self links and links
mixing two revisions of the same repository are rejected. Revision succession is
represented by separate node identities, not an execution relationship across time.

Each link references existing immutable Evidence in the admitted tenant/repository
and retention window. The original record preserves producer, version, status,
confidence and source information. Both endpoints and the source evidence must
exist no later than the assertion timestamp. `OBSERVED`, `INFERRED` and `UNKNOWN`
bases describe attribution; even an observed `CAUSES` assertion is not a proof of
causality. These records cannot produce PASS, erase contradictory evidence or
authorize production. Trusted orchestration validates provenance before insertion.

## Operations and persistence

- `PutChangeNode` inserts a sealed revision-specific node. Exact retries succeed;
  different canonical bytes for the same identity return `ErrConflict`.
- `GetChangeNode` reads only the requested pinned revision, revalidates the model,
  hash, indexed identity and timestamps, and applies repository/retention scope.
- `PutChangeLink` validates the source Evidence and both revision-qualified
  endpoints before insertion. Exact retries succeed and replacements conflict.
- `ChangeNeighbors` returns an incoming, outgoing or bidirectional adjacency page,
  optionally filtered by relation. Both endpoints and supporting evidence must be
  visible; edges never disclose hidden repositories or alternate revisions.

Apply additive migration `000007_causal_change_graph.up.sql` after migrations
000001–000006. It creates `causal_change_nodes` and `causal_change_links`, scope and
adjacency indexes, tenant-qualified foreign keys, canonical/index identity checks
and append-only triggers using the existing graph trigger function. It does not
change Evidence, edition assignments, bundle configuration or quota accounting.

Writes use READ COMMITTED transactions and insert/compare retries without upserts
that modify history. Readers use parameterized SQL and validate returned envelopes.
The Go boundary computes domain-separated SHA-256 content hashes; they detect
corruption, not malicious replacement by a database administrator. As with
EvidenceGraph, trusted composition owns driver registration, primary database
connection, fixed search path, credentials and context deadlines. Application roles
should have SELECT/INSERT only; scanner processes must never receive database access.
There is no RLS or protection against privileged administrators disabling triggers.

All graph timestamps use UTC microseconds. Evidence retains its original bytes and
nanoseconds; its indexed time is consistently truncated when compared with links.
Paths are validated as portable repository-relative metadata and never opened.
No URLs are fetched, processes launched, symlinks followed or archives extracted.
JSON rejects duplicate keys, invalid Unicode, NUL and excessive nesting. Payloads
are bounded at 64 KiB and full nodes at 128 KiB; immutable constructors and getters
copy data. These are shared resource bounds, not commercial defaults.

Queries admit at most 64 repositories and 100 links per page. `AfterLinkID` is an
exclusive C-collated cursor; `NextCursor` indicates another row was found. A cursor
is not authorization or a transaction snapshot. Concurrent appends may affect later
pages. Reauthorize every page and retain the caller's explicit revision pins.

## Admission

`application.ChangeGraphService` wraps every operation with the existing
`GraphAccess`: resolve repository ownership, check feature grants, independently
check `proof.view` or `proof.execute`, and load current `retention_days` policy.
The graph view is fixed by trusted service composition. Request bodies and queued
jobs cannot supply an already-authorized scope. Workers call this service at start
using the authenticated principal and pins from trusted work records.

| View | Required capabilities |
| --- | --- |
| Local, one repository | `graph.local` |
| Organization, one repository | Above plus `graph.organization` |
| Organization, multiple repositories or explicit cross-repository view | Above plus `graph.cross_repo` |

Grant failures, ownership mismatches, invalid/disabled retention, permission denials
and dependency errors fail closed. Restricting retention changes visibility without
deleting history; restoring access exposes preserved data. No edition branches,
new grants, bundle changes or premium implementation variants are introduced.
Local describes single-repository scope; this PostgreSQL adapter does not simulate
an offline hosted graph.

```go
service, err := application.NewChangeGraphService(access, postgres, application.GraphCrossRepository)
if err != nil { return err }
page, err := service.Neighbors(ctx, actor, organizationID,
    []graph.RevisionPin{
        {RepositoryID: "api_repo", Revision: apiCommit},
        {RepositoryID: "worker_repo", Revision: workerCommit},
    }, graph.ChangeQuery{
        Root: graph.EntityRef{RepositoryID: "api_repo", Revision: apiCommit, EntityID: "handler"},
        Direction: graph.Both, Limit: 50,
    })
// Handle err before using page; render graph metadata as untrusted text.
```

## Validation and remaining boundaries

Tests cover all entity/relation kinds, immutable encoding and concurrent reads,
revision separation, malicious metadata, SQL bindings, transaction rollback,
idempotent retries, source/endpoint visibility, pagination and poisoned result rows.
Application tests exercise grants, independent authorization and revocation on all
four operations; retention downgrade/restore preserves the original content hash.
Architecture checks prevent graph semantics from importing commercial admission.

`TestPostgresCausalChangeGraphMigration` uses the existing opt-in native harness.
It checks additive history preservation, both revisions of an entity, foreign keys,
all kinds/relations, canonical projections, rejected mutation and retention changes.
Configure a disposable PostgreSQL database through libpq settings and install psql:

```powershell
$env:SAFEPATCH_POSTGRES_MIGRATION_TEST = '1'
go test ./internal/platform/migrationtest -run TestPostgresCausalChangeGraphMigration -count=1 -v
```

Without that configuration native tests skip. Scripted database/sql tests do not
validate PostgreSQL query execution, locks or concurrent transaction behavior.
Always run `go test ./...` and `go test -race ./...` as well.

Automatic SIR/runtime/incident ingestion, revision-completeness manifests, multi-hop
impact traversal, automatic causal inference, HTTP/CLI routes and production worker
composition remain separate work. Retention is an access window, not physical
purging. This milestone does not claim an end-to-end graph product is available.
