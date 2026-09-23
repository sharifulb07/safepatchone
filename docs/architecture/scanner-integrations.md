# External scanner adapters

`integrations/scanners` integrates Gitleaks, Semgrep, Trivy, OSV-Scanner and Syft
through the existing Verifier SDK and secure Runner. Detection remains in the
upstream engines. There are no edition-specific adapters or engines, and this
milestone adds no CLI command, hosted API endpoint, queue consumer or billing
integration.

## Contracts and execution

`Adapter` extends `verification.Verifier` with `Tool()`, `ToolVersion()` and
`Environment()`. `New(tool, Config)` creates one immutable adapter. The SDK ID
and capability are both `scanner.<tool>`; the adapter version is `1.0.0`.
Tool versions are separate operator-supplied installation metadata. They are
not inferred from repository content and are not binary attestations.

| Tool | Command / output | Provisioning |
| --- | --- | --- |
| Gitleaks 8.x | `dir`, JSON report on stdout | Trusted TOML rules and an empty ignore file |
| Semgrep 1.x | `scan`, OSS engine, JSON | Trusted local rules file; no registry URL or account |
| Trivy 0.x | `filesystem`, vulnerability JSON schema 2 | Trusted config, empty ignore file, provisioned vulnerability/Java databases |
| OSV-Scanner 2.x | `scan source`, offline JSON with all packages | Trusted global TOML override and offline ecosystem databases |
| Syft 1.x | `scan dir:.`, Syft JSON schema 16.0.0 or 16.1.0 | Trusted config |

The version families describe accepted configuration, not a claim that every
release in a family is compatible. Pin and smoke-test the actual binaries and
rules/databases together. Unsupported CLI flags or JSON schemas fail closed.

`Config.Executable` is a Runner tool ID. The Runner maps it to a trusted absolute
executable path. Config, ignore and database paths must be absolute, outside the
source tree and, for containers, outside `/workspace` and `/input`. Missing tool
IDs or required configuration produce UNAVAILABLE without executing anything.
Invalid installed files or databases fail through the Runner/tool result.

The caller supplies a complete `Target.Files` manifest for the same immutable
snapshot as `BaseExecution.SourceDirectory`, and sets `BaseExecution.Environment`
to `adapter.Environment()`. Configure exactly those variable names in the
Runner's allowlist. Plans retain the caller's execution mode, source and resource
limits and always scan the copied source root. No arbitrary argument overrides,
shell command strings, URLs, installation steps, autofixes or image pulls are
accepted. The SDK enforces the planned invocation.

Gitleaks scans the current file snapshot, not Git history; inline allow comments
are disabled and archive/recursive decoding is disabled. Semgrep disables metrics,
version checks, inline suppression, Git ignores and secret validation. A manifest
containing any `.semgrepignore` is rejected: stable CLI flags cannot safely
override nested ignore files. Trivy selects vulnerability scanning and disables
updates, telemetry and online dependency lookup. OSV disables online operations,
dependency resolution and Go/Rust call analysis. Syft disables update checks,
remote license lookup, local external language caches and package archive searches.
Trusted config contents still determine the scan scope and must be reviewed;
these adapters do not interpret or validate upstream rule languages.

Use `ContainerSandboxRunner` for untrusted sources, including local sources.
Provision scanner binaries, trusted configs and offline databases in its pinned
image; the existing Runner does not mount arbitrary resource directories. The
container has no network, a read-only root, non-root identity and bounded writable
workspace/temp storage. Hosted execution must always select Sandbox in trusted
server/worker composition. `LocalRunner` is only for explicitly trusted tools and
sources; it does not enforce filesystem or network isolation. Never mount the
evidence archive into a scanner container.

## Original evidence

Run through `scanners.Executor`, not directly through `Registry.Run`, when original
evidence is required:

```go
adapter, err := scanners.New(scanners.Gitleaks, scanners.Config{
    Executable: "gitleaks",
    Version: "8.24.2", // must match the provisioned binary
    ConfigFile: "/opt/safepatch/rules/gitleaks.toml",
    IgnoreFile: "/opt/safepatch/rules/empty-ignore",
})
// Handle err. Compose secureRunner and originalStore with trusted policy.
executor, err := scanners.NewExecutor(adapter, secureRunner, originalStore, 5*time.Second)
// Handle err, resolve identity/tenant, enforce entitlement, permission and quota.
input.BaseExecution.Environment = adapter.Environment()
result, err := executor.Run(ctx, input, evidence.ArchiveScope{
    OrganizationID: admittedOrganizationID,
    Subject: input.Target.Subject,
})
// Handle err and result.Record.Report.Status separately. Only PASS is success.
```

`evidence.OriginalStore` exposes scoped immutable `Put`/`Get`. `Executor` stores
stdout and stderr byte-for-byte **before** normalization, then stores a manifest
binding their references, process status, truncation flags, subject, configured
tool version and normalized record. It returns content references, not raw output
or public artifact URLs. Digests agree with the SDK execution record. Whitespace,
malformed JSON and binary bytes are preserved. The Syft stdout object is the
complete original SBOM, not just the normalized package summary.

Timeout, cancellation and output-limit results preserve whatever bounded bytes
the Runner actually captured. Truncation is explicit; discarded bytes cannot be
recovered or represented as complete evidence. Persistence uses a separate,
operator-bounded timeout after cancellation. Failed original or manifest writes
force ERROR and an error return. Store failure may leave unreferenced objects;
no background deletion or retention policy is invented here.

`internal/platform/evidencestore.NewLocal` binds an already-authorized scope to a
private directory. Content-addressed objects are written and flushed before
atomic hard-link publication; existing bytes are never replaced. Reads verify
size and SHA-256. Scope-specific `os.Root` handles confine filesystem access;
keys cannot contain scanner paths, and a handle rejects other organizations,
repositories or revisions. Concurrent identical writes are idempotent. Filesystems
must support hard links; unsupported publication fails closed. Directory fsync
is performed on Unix; Windows power-loss durability follows its filesystem's
directory-metadata guarantees.

Original output can contain credentials, source snippets and private dependency
data. It is deliberately **not redacted in storage**, since redaction would alter
the evidence. The archive root and parents must be administrator-controlled,
outside source directories, and protected by OS ACLs; Unix objects use 0600 and
scope directories 0700. The local adapter does not provide application encryption
at rest. Normalized findings use fixed messages and hashed upstream IDs, omit
secret/match fields and omit absolute or traversing paths. Do not log, render or
download original evidence without independent access checks and appropriate
escaping/redaction. Production cloud storage must implement this same contract
with private S3-compatible objects, encryption, retention and authorized retrieval.
That cloud storage adapter is outside this milestone.

## Normalization and admission

All tools use `safepatch.verification.v1`. Strict bounded parsing rejects duplicate
keys, consumed-field case aliases, invalid Unicode, excessive nesting, malformed
required fields and contradictory exit/report combinations. Normalization never
uses scanner text to set the repository identity or authorize production.

Findings from the four security scanners produce FAIL. Tool failures produce
ERROR; partial scan diagnostics and stderr conservatively produce UNKNOWN.
Empty inventories or no scanned Semgrep files produce SKIPPED. A valid empty
Gitleaks findings array with a successful process is a clean snapshot scan.
Syft PASS means SBOM generation succeeded; it makes no vulnerability claim.
No status implies universal repository coverage: upstream supported formats,
trusted rule selection and explicit scope restrictions still apply.

`application.ScannerVerificationRequirement` resolves every scanner capability
to the existing FeatureCatalog key `verify.security` and independent permission
`proof.execute`. This grant is already present in the immutable Community bundle;
no bundle or migration changes are needed. The location is trusted composition
metadata, not an edition name or a user-controlled route to cheaper admission.

Local Community operation uses `CommunityEntitlementProvider` with its local
namespace and configured installed engines. It needs no subscription or account.
Hosted operation must use authenticated identities, a resolved real tenant and
its authoritative entitlement service. The offline provider rejects hosted tenant
IDs and must never serve as a cloud fallback. Existing `WorkerOperation.Start`
reloads work and rechecks entitlement, permission and quota before execution.
Cloud policy must reserve/settle its configured proof-credit, concurrency and
artifact-storage limits with the shared quota infrastructure. No numeric quota
or edition logic is embedded in these adapters.

## Validation and live provisioning

Run `go test ./...` and `go test -race ./...`. Tests cover all five protocols,
Runner-backed fixture processes, exact original bytes, malformed/partial output,
archive failures, cancellation, tampering, scope isolation, concurrent publication,
and entitlement/permission/quota boundaries. Grant revocation and restoration
leave stored work and immutable evidence unchanged. Parser fuzz seeds are included;
an optional campaign is `go test ./integrations/scanners -run '^$' -fuzz FuzzNormalizeScannerOutput -fuzztime 10s -parallel 2`.

For each installed tool, set `SAFEPATCH_<TOOL>_EXECUTABLE` to its absolute binary
path and `SAFEPATCH_<TOOL>_VERSION`, `_CONFIG`, plus `_IGNORE` for Gitleaks/Trivy
and `_DATABASE` for Trivy/OSV. TOOL is GITLEAKS, SEMGREP, TRIVY, OSV or SYFT.
`SAFEPATCH_SCANNER_TOOL_PATH` optionally supplies a platform-separated list of
trusted helper-tool directories. Then run:

```powershell
go test ./integrations/scanners -run TestInstalledScanners -v -count=1
```

Unconfigured engines explicitly skip; configured engines must complete an actual
scan of a temporary fixture and preserve their original report. This smoke test
uses trusted local fixtures, not container isolation validation. No upstream
engines or databases are downloaded by tests. CLI/server/worker runtime composition,
image scanning, Git history scanning and hosted storage are not added here.

## Upstream contracts

- [Gitleaks commands and configuration](https://github.com/gitleaks/gitleaks/blob/master/README.md)
- [Gitleaks stdout report target](https://github.com/gitleaks/gitleaks/blob/master/report/report.go)
- [Semgrep CLI reference](https://docs.semgrep.dev/cli-reference)
- [Trivy filesystem CLI](https://trivy.dev/docs/latest/references/configuration/cli/trivy_filesystem/)
- [OSV source-scanning usage](https://github.com/google/osv-scanner/blob/main/docs/usage.md), [offline mode](https://github.com/google/osv-scanner/blob/main/docs/offline-mode.md), [JSON output](https://github.com/google/osv-scanner/blob/main/docs/output.md)
- [Syft configuration](https://oss.anchore.com/docs/reference/syft/configuration/), [Syft JSON schema](https://github.com/anchore/syft/blob/main/schema/json/schema-16.0.0.json)
