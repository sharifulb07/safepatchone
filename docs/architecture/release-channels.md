# Release channels independent of edition

The subsequent [download distribution milestone](download-distribution.md) wires
authenticated short-lived S3 downloads into the shared API. The streaming
ReleaseArtifacts contract below remains available for consumers that need bytes.

The shared `domain/release` package defines exactly `stable`, `beta`, `nightly`
and `lts`. Product versions remain numeric `major.minor.patch`; neither edition
nor channel is encoded in that version. Channels are explicit trusted catalog
assignments. The same version can be listed under multiple channels and still
use the same source, Go binaries, verification engine and migration history.

| Channel | Organization entitlement | Actor permission |
| --- | --- | --- |
| stable | `verify.basic` | `organization.manage` |
| beta | `verify.basic` | `organization.manage` |
| nightly | `verify.basic` | `organization.manage` |
| lts | `enterprise.lts` | `organization.manage` |

These are the existing release-management permissions, not a new edition policy.
An Enterprise organization without `enterprise.lts` is denied LTS, while another
edition with an explicit valid grant can use it. Community + stable, Developer +
beta, Team + stable, Business + stable, Enterprise + stable and entitled Enterprise
+ lts therefore use identical code. Choosing beta/nightly does not weaken tenant,
verification, integrity or production-authorization checks.

## Catalog and access

Provide the shared `NewReleaseService` with trusted `Release` records and the
existing PlatformAccess dependencies. For example, catalog records can associate
version `1.5.2` with each of the four channels, with independently configured
`supported_until` deadlines. Records contain no edition. Construction copies
records, validates canonical channel/version/deadline and rejects duplicate
version/channel identities. Historical uppercase `LTS` is accepted and normalized;
other case variants, whitespace, unknown channels and empty values are rejected.
`LTS` plus `lts` in the same catalog is a duplicate, not two channels. The shared
OpenAPI `ReleaseChannel` enum documents canonical output values.

`Eligible` and `SupportEligible` resolve tenant membership, current entitlement,
actor permission and quota before returning an unexpired catalog record. A
caller cannot grant access by submitting a channel, edition or version. Unknown
versions/channels and entitlement-provider errors fail closed. At the exact
support deadline the record is unavailable; there is no silent channel fallback.

`NewReleaseArtifacts(releases, source)` wraps a trusted private
`ReleaseArtifactSource`. Its `Open(ctx, actor, org, version, channel, name)` calls
the same admission before opening storage. Denied requests never call the source.
Source failures are sanitized, canceled requests are rejected and filenames cannot
contain paths, URLs, encoded traversal or control characters. The source must
resolve immutable bytes bound to the admitted version/channel, honor cancellation
and keep its bucket, credentials and URLs private. Caller-supplied storage URLs
are not accepted. API composition must supply an authenticated principal and
stream the authorized reader; it must not expose a public origin that bypasses
this service. The caller owns closing a successfully opened reader.

Downgrades revoke subsequent artifact/support admissions without rewriting
artifacts, catalog entries or migration history. Already opened streams are not
retroactively revoked. Channel choice does not change a customer's subscription,
license, edition bundle, retention configuration or deployed software. Channel
catalog storage and updates remain trusted deployment configuration.

## Shared release mechanics

The existing [release pipeline](../releases.md) builds one versioned artifact set.
There are no channel-specific source branches or builds. Packager `-channel`
defaults to stable and records a canonical label in `release.json`; it does not
enter linker flags or grant runtime capabilities. For example:

```powershell
go run ./tools/release -version 1.5.2 -channel beta -commit $releaseCommit -timestamp $releaseTimestamp -out beta-candidate
go run ./tools/release -version 1.5.2 -channel lts -commit $releaseCommit -timestamp $releaseTimestamp -out lts-candidate
```

Both use the same six executables and compiler inputs. ZIP manifests/checksums
can differ because the distribution label differs. A trusted operator assigns
an immutable version to a runtime channel; a channel is not inferred from a Git
tag or commercial edition. Do not route restricted LTS artifacts through the
public release/registry pipeline. Their private distribution adapter must be
behind ReleaseArtifacts. This milestone supplies the admission contract, not
credentials or a concrete HTTP/S3 download endpoint. Scheduling nightly builds,
automatic updates and support ticket integrations are separate tasks.

## Task report

Inspected the existing release service and capability tests, shared packager/tests,
workflow, runtime edition configuration, architecture rules, release documentation
and the one API specification. Reused PlatformAccess/ExecutionGuard, current
entitlement and permission services, numeric versions and the existing packager.

Created `internal/domain/release/channel.go`, `channel_test.go`,
`internal/application/release_artifacts.go`, `release_channels_test.go` and this
document. Modified `internal/application/platform_capabilities.go`, its tests,
`tools/release/main.go`, its tests, `api/openapi/openapi.yaml`, `docs/releases.md`
and `docs/architecture/platform-capabilities.md`. No database migration, module
dependency, new entitlement key, edition bundle or release workflow was introduced.

Tests exercise all five editions across four channels, explicit LTS grants and
denials, authorization after entitlement, tenant rejection, downgrade/restoration,
catalog/data preservation, exact expiry, canonical aliases, duplicate catalog
entries, storage errors, cancellation, unsafe artifact names and channel injection
in packaging metadata. Actual hosted artifact/support delivery remains a
composition boundary; no live release, private storage or support system was changed.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed. Two complete cross-platform packaging runs used identical
synthetic version/commit/time inputs with `beta` and legacy `LTS` channel inputs.
All 36 executable hashes matched across the resulting six pairs of archives;
only distribution metadata changed, with `LTS` normalized to `lts`. These are
local validation artifacts, not published releases.

Next recommended prompt: implement the authenticated release-download endpoint and
private immutable artifact source using ReleaseArtifacts, with end-to-end tests
proving public storage cannot bypass LTS entitlement and tenant admission.
