# Semantic Intermediate Representation

`internal/sir` defines SafePatch's one language-neutral Semantic Intermediate
Representation (SIR). Language adapters supply extracted concepts; explicit
normalization mappings translate their native kinds into shared semantic kinds.
The same versioned schema serves Community, Developer, Team, Business and Enterprise.

## Semantic kinds

| Kind | Meaning |
| --- | --- |
| `function` | A callable function declaration |
| `method` | A callable associated with a receiver or owning type |
| `class` | A class declaration |
| `type` | A type declaration, including adapter-classified aliases/interfaces/structs |
| `api` | An exposed interface or endpoint |
| `dependency` | A package, module, library or service dependency |
| `db_operation` | A database operation |
| `network_operation` | Network communication |
| `filesystem_operation` | Filesystem interaction |
| `process_operation` | Process creation, execution or control |
| `concurrency_operation` | Scheduling, synchronization or concurrent communication |
| `test` | A test declaration |
| `side_effect` | An explicitly identified effect not represented more specifically |

Kinds are closed and contain no edition, subscription, capability or permission
concepts. A class named `Enterprise` is ordinary source data, not an edition switch.
There is no extensible metadata bag through which commercial fields can become part
of the shared schema. Architecture tests reject SIR imports of edition, entitlement,
feature and permission packages as well as infrastructure dependencies.

## Snapshot and node model

`sir.Fields` is the mutable construction/inspection DTO. `sir.Representation` seals
it into private canonical JSON with no mutators. Its top-level fields are:

- `schema`: `safepatch.sir.v1`.
- `tenant_id`, `subject`: scoped repository identity and immutable source revision,
  reusing the existing `evidence.Subject` model.
- `producer`, `producer_version`: extractor identity and semantic version.
- `deterministic`: extractor's trusted determinism claim.
- `coverage`: explicit `UNKNOWN`, `PARTIAL` or `COMPLETE` extraction coverage.
- `files`: repository-relative paths and SHA-256 hashes of their source bytes.
- `nodes`: normalized semantic nodes.

Coverage is extraction metadata, not verification status. The zero value is invalid;
callers must explicitly state UNKNOWN when completeness is unknown. Normalization
does not upgrade coverage or determinism. Complete means the admitted extractor
claims coverage of its declared extraction scope and supported concepts; the model
cannot prove full program understanding or missing side effects. It contains no
PASS or production approval field. An empty node list is not proof of safe behavior.

Each node has `id`, `kind`, `language`, `native_kind`, `name`, `source`, `parent_id`,
`target_id`, `target` and `action`. Source uses the shared SourceLocation coordinates:
positive positions are one-based and end coordinates inclusive; zero positions mean
unknown or whole-file locations under the same validation rules as Evidence.
Adapters must use consistent coordinate conventions for their consumers.

Every source path must occur in the hashed file manifest. Node IDs are unique within
the snapshot; they are producer-assigned identities, not global authorization tokens
or a guarantee of stability across revisions. Native kind/language preserve extractor
provenance while the normalized kind carries shared meaning.

`parent_id` represents optional lexical/structural containment. A parent must exist
in the snapshot and be a function, method, class, type, API or test. Containment
cycles are rejected by iterative validation; cross-file containment is permitted
for constructs such as split declarations. No source-range containment is inferred.

`target_id` references another local node. Dangling references are invalid; recursive
target links are legal. `target` preserves a bounded external symbol or resource
description as inert text. Empty target fields mean unresolved, not absent effects.
Resolution across snapshots/tenants must happen through a separately authorized
resolver; a target string is never automatically dereferenced.

The six operation/effect kinds require an `action` token such as `read`, `write`,
`connect`, `spawn` or `lock`. Other kinds leave it empty. Tokens are explicit adapter
semantics rather than executable commands. This milestone standardizes concept
kinds; it does not claim a complete cross-language action vocabulary or infer effects
from names. Adapters must document their action mappings.

## Language normalization

`NewNormalizer([]Mapping)` seals a private mapping from `(language, native_kind)` to
a shared Kind. Configuration comes from trusted adapter composition, never repository
text or an LLM-selected cheaper classification. Every incoming concept must have a
mapping. Duplicate mappings, invalid tokens and unknown kinds are errors.

```go
normalizer, err := sir.NewNormalizer([]sir.Mapping{
    {Language: "go", NativeKind: "func_decl", Kind: sir.Function},
    {Language: "go", NativeKind: "receiver_method", Kind: sir.Method},
    {Language: "go", NativeKind: "struct_decl", Kind: sir.Type},
    {Language: "typescript", NativeKind: "function_declaration", Kind: sir.Function},
    {Language: "typescript", NativeKind: "class_declaration", Kind: sir.Class},
})
if err != nil { return err }
snapshot, err := normalizer.Normalize(ctx, extraction)
if err != nil { return err }
raw, err := snapshot.MarshalJSON()
if err != nil { return err }
// Authorize the matching tenant/repository/revision before storing raw.
restored, err := sir.Decode(ctx, raw)
if err != nil { return err }
fields, err := restored.Fields()
// Consume normalized facts, respecting extraction coverage and provenance.
```

`Extraction` supplies Metadata, Files and Concepts. It has no normalized kind field
that can override the configured mapping. Unknown native concepts return
`ErrUnknownConcept`, with no partial output or silent coercion to type/function.
The adapter must model unsupported extraction explicitly and mark partial/unknown
coverage before supplying a supported extraction scope; it must not discard relevant
facts to manufacture complete coverage.

`New(ctx, Fields)` accepts already-normalized trusted adapter output and applies the
same document validation. Mapping rules are not language parsers. This milestone
adds neither AST extraction nor SQL/network detection engines; language adapters
remain responsible for extracting facts and preserving their source identity.

## Canonicalization and security

Files are sorted by path and nodes by ID. Duplicate files/nodes are rejected.
Canonical bytes bind all metadata, file hashes and node fields. The content hash is:

```text
SHA256(UTF8("safepatch.sir.v1\n") || canonical JSON)
```

Returned JSON and Fields are independent copies. Concurrent reads of a sealed
Representation and normalization through a shared immutable Normalizer are safe.
Callers must not mutate construction DTOs concurrently with normalization. Changed
facts or producer versions create new artifacts; existing artifacts are not rewritten.

`Decode` reuses the bounded JSON validator and requires exact fields, including
explicit empty strings/arrays. It rejects duplicate keys, case aliases, nulls,
unknown fields (including commercial fields), malformed Unicode, invalid references,
unsupported schemas and trailing documents. Direct `json.Unmarshal` into a sealed
Representation returns `ErrImmutableRepresentation`.

Safety bounds are 2 MiB serialized/input JSON, 4,096 nodes, 1,024 files and 512 mapping
entries. Paths are at most 512 bytes, names 1,024 bytes, external targets 2,048 bytes,
and language/native-kind/action tokens 64 bytes. These are shared resource bounds,
not commercial quotas. Controls, format controls, unsafe relative paths and invalid
hashes are rejected. Errors do not echo supplied source text or credentials.

The core performs no filesystem access, process execution, URL fetching or archive
extraction. Paths cannot traverse outside a logical repository namespace; actual
symlink confinement remains the upstream Runner/snapshot adapter's responsibility.
Target text may contain shell/SQL/URL syntax but remains data. Do not put secrets
into names/targets: the representation preserves supplied text and is not a redactor.

Hashes provide integrity identity, not signatures or authentication. Trusted
orchestration must bind extractor claims to the admitted tenant/source snapshot.
The existing scoped immutable artifact store supports serialization without a new
database schema. Retrieval/storage authorization and retention remain outside SIR.

## Scope and validation

Tests cover all thirteen kinds with Go/TypeScript/Python adapter mappings, canonical
ordering, defensive copies, unknown coverage, unmapped concepts, unsafe inputs,
strict JSON/fuzz seeds, source bindings, duplicate/dangling references, containment
cycles, recursive target links, deep iterative validation and concurrent reads.
Integration tests normalize mixed-language facts, persist/reload them through the
existing immutable store, enforce tenant/repository/revision isolation, and preserve
identical schema/history for all five edition labels.

No premium operation, entitlement grant, edition bundle, migration, API/CLI endpoint,
language extractor or SIR-to-IntentGuard change analyzer is introduced. Existing
IntentGuard mappings remain explicit inputs; deriving changes between SIR snapshots
is a separate milestone. Existing migration regression tests remain in the full
suite; native PostgreSQL validation keeps its opt-in environment requirement.
