# One SafePatch release pipeline

Public and protected delivery is implemented by the shared
[downloads service](architecture/download-distribution.md), including private S3
presigning after current organization entitlement and artifact authorization.

`.github/workflows/release.yml` builds the six existing Go entrypoints from one
commit and module. Every platform ZIP contains `safepatch`, `safepatch-api`,
`safepatch-worker`, `safepatch-collector`, `safepatch-mcp-gateway` and
`safepatch-replay`, plus `release.json`. Windows executables have `.exe` suffixes.
Linux, macOS (`darwin`) and Windows each receive amd64 and arm64 archives.

The exact same artifacts serve Community, Developer, Team, Business and
Enterprise. There is no edition build flag, source selection or release matrix.
Inherited Go flags/workspaces are cleared by the packager. Product version, source
commit and source timestamp are linker metadata, exposed by every binary through
`version --json` or `--version`. They confer no entitlements. Stable/LTS eligibility
continues to use the existing runtime ReleaseService and capability checks;
release channels do not fork the binaries or schema. The four canonical keys are
`stable`, `beta`, `nightly`, and `lts`; see [release channel admission](architecture/release-channels.md).

`safepatch version` also reports protocol, edition configuration and entitlement
source as separate fields; see [version inspection](architecture/version-command.md)
for the local defaults and trusted organization metadata interface.

## Running the pipeline

Commit SafePatch as the root of its single repository, with `go.mod`, source,
workflow and ordered migrations tracked. Configure the GitHub `release`
environment to restrict deployment to protected numeric version tags, and
restrict who may create or move those tags. Enable the workflow's requested
GHCR package, attestation and OIDC permissions. Do not grant these permissions to
untrusted pull requests. The workflow has no pull-request publishing trigger.

Use `workflow_dispatch` to validate an unsigned `0.0.0` snapshot without creating
container packages or a GitHub release. For a real candidate, create and push a
protected tag such as `v1.5.2` on the reviewed commit. Only `vMAJOR.MINOR.PATCH`
is accepted. The workflow:

1. Verifies Go modules, runs the full test/race/vet suites and actionlint.
2. Cross-compiles 36 binaries using the Go version from `go.mod`, CGO disabled,
   `-trimpath`, explicit build identity and no edition tags. Existing output
   paths are refused. ZIP timestamps and executable modes are normalized.
3. Scans the unpacked compiled binaries for one SPDX JSON SBOM using pinned Syft.
   The inventory paths identify platform and binary; Go build information supplies
   dependencies. This is a software inventory, not a vulnerability or license scan.
4. Creates signed build provenance in the build job and uploads its bundle with
   the archives. Native Linux amd64, macOS arm64 and Windows amd64 runners execute
   the downloaded release binaries and inspect local entitlements.
5. Reuses the archived Linux binaries in six images at
   `ghcr.io/<owner>/<repository>/<command>:v1.5.2`, each supporting amd64/arm64.
   One digest-pinned distroless base supplies CA certificates. The shared Dockerfile
   runs as non-root, copies no source and uses a fixed entrypoint. BuildKit attaches
   image SBOM/provenance, Cosign signs image digests, and GitHub attests them.
6. Combines image digest receipts, provenance bundles, SBOM, manifest and ZIPs into
   a SHA-256 manifest. Cosign signs and verifies that manifest with GitHub OIDC.
   Only after all jobs succeed does it create a **draft** GitHub release containing
   the complete set. Review and publish that draft as the final promotion step.

The default token is scoped per job; only image jobs write packages, and only
the final job writes releases. Actions are pinned to full commit SHAs. Syft and
Cosign versions and the base image digest are explicit. GitHub runner images,
Buildx/BuildKit defaults and its SBOM generator remain hosted toolchain inputs;
provenance records those executions. This workflow does not claim hermetic builds
or SLSA level 3. Review tool pins periodically through the same verification process.

Failed signing, SBOM generation, tests, image publication or checksum validation
prevents draft creation. Parallel image jobs can leave individually signed images
if a later job fails; use the completed draft's digest receipts as the release
inventory. Tags must not be moved or rebuilt as replacement releases. A rerun
will refuse to overwrite an existing draft; investigate or issue a new version.

## Local packaging

With a clean, committed checkout and the intended Go toolchain, run from its root:

```powershell
$releaseCommit = git rev-parse HEAD
$releaseTimestamp = git show -s --format=%cI HEAD
go run ./tools/release -version 1.5.2 -channel stable -commit $releaseCommit -timestamp $releaseTimestamp -out dist
```

The standard-library packager creates six ZIPs, `release.json` and `checksums.txt`.
`-channel` selects distribution metadata in `release.json` only; it never changes
linker flags or the six compiled programs. Use separate fresh output directories
when packaging the same version for different channels. Omission defaults to
`stable`; legacy `LTS` is normalized to `lts`. Unknown or empty explicit channel
values fail validation. The existing tag pipeline publishes versioned build sets;
channel assignment is trusted catalog configuration, not an edition/tag mapping.
Do not publish restricted LTS artifacts to public GitHub or registry assets:
deliver them through the private bucket and guarded
[downloads API](architecture/download-distribution.md). The public version pipeline
is not a licensed-download API.
It deliberately does not sign local outputs or label them authoritative CI
provenance. Use a fresh output directory for each run. Equal inputs and the same
Go toolchain yield stable archive headers; cross-toolchain binary reproducibility
is not promised. SBOM generation, attestations, containers and signing run in CI.

## Verifying a downloaded release

Download the chosen ZIP, `checksums.txt`, `checksums.sigstore.json` and provenance
bundle from the same release. Pin the expected repository, tag and workflow
identity rather than accepting any valid Sigstore signer:

```sh
repo=OWNER/REPOSITORY
tag=v1.5.2
cosign verify-blob --bundle checksums.sigstore.json \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
# With all listed assets downloaded:
sha256sum --check checksums.txt
gh attestation verify safepatch_1.5.2_linux_amd64.zip --repo "$repo"
cosign verify --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$tag" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "ghcr.io/$repo/safepatch-api@sha256:EXPECTED_DIGEST"
```

Keep the signature bundles for offline verification using Cosign's offline
verification option. Release signing keys are ephemeral OIDC identities; they
are unrelated to commercial license-signing keys. No private signing key is
embedded in any node or archive. Air-gapped runtime entitlement resolution still
uses the existing offline signed license and never requires release-service access.

Implementation references: [GitHub artifact attestations](https://github.com/actions/attest),
[Cosign blob signatures](https://docs.sigstore.dev/cosign/signing/signing_with_blobs/),
[Docker SBOM/provenance](https://docs.docker.com/build/ci/github-actions/attestations/),
and [GitHub native runner platforms](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).

## Scope and validation report

This milestone packages existing functionality. Worker, collector, MCP gateway
and Replay entrypoints still expose their existing command shells; packaging
does not add daemon composition. Images default to `--help`, not a fabricated
healthy service. The API supports `serve`; deployments must supply its existing
configuration and set an appropriate listen address. Do not deploy a shell image
as an operational worker. No installer, macOS notarization, Windows Authenticode,
new feature grants, database migrations or alternate release stream is added.

Inspected: all six `cmd/*/main.go`, shared `application/command.go` and CLI output,
runtime ReleaseService, edition configuration, architecture/entrypoint tests,
`go.mod`, `go.sum`, `.gitignore`, README and deployment documentation. No previous
release workflow or Dockerfile existed.

Created: `.github/workflows/release.yml`, `deploy/Dockerfile.release`,
`tools/release/main.go`, `tools/release/main_test.go`,
`internal/application/build_info.go`, `build_info_test.go`, and this document.
Modified: `.gitignore`, `internal/application/command.go`, README and deploy README.
No module dependency or API/schema change.

Validation on 2026-09-07: full Go tests, race tests, vet and pinned actionlint passed.
All 36 binaries were locally cross-compiled using Go 1.26.5; all six archives and
their checksums were verified. All six Windows amd64 release binaries passed
version/help checks and the CLI inspected its offline entitlements. Unit tests
cover metadata injection, hostile build flags, archive allowlisting (including
private-key exclusion), missing inputs, overwrite refusal, stable ZIP output and
build identity across all five editions. Existing entitlement and migration tests
remain in the full suite; native PostgreSQL tests retain their opt-in skip.

The local packaging run used synthetic version/commit metadata for validation,
not an official release. Docker, Syft and Cosign were not installed locally; no
image, SBOM, remote signature, attestation or GitHub release was published. Native
Linux/macOS execution and the complete OIDC/GHCR workflow await a configured CI run.
Next recommended task: run this workflow in the protected GitHub repository,
verify the draft assets/image digests independently, and address service command
composition in a separate milestone.
