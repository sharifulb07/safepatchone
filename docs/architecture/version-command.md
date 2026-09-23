# Version inspection

Run `safepatch version` (alias `safepatch --version`). All six shared binaries
support this command. `--json` emits `version`, `protocol`, `edition`,
`edition_configuration`, `release_channel`, and `entitlement_source`, plus the
existing `commit` and `built_at` fields used by release checks. JSON uses canonical
lowercase edition, channel and source keys; text uses catalog edition names.

For a trusted Team subscription context and a 1.0.0 build, text is:

```text
SafePatch:
1.0.0

Protocol:
1

Edition:
Team

Edition configuration:
team-2026-v1

Release channel:
stable

Entitlement source:
subscription
```

The current standalone CLI uses the existing offline Community provider and its
actual bundle ID (`community-2026-v2`), source `community_default`, and default
channel `stable`. An unstamped development build reports `dev`. It does not pretend
to have a paid organization session. `SAFEPATCH_EDITION` and repository configuration
cannot override these values, and the command makes no network requests.

Organization-aware composition roots can supply `VersionMetadataProvider` through
`RunWithVersionMetadata`. That provider must resolve authenticated organization
state from the existing subscription, trial or verified license services and a
trusted deployment channel. It is queried for each inspection. Invalid or unavailable
metadata fails without a Community fallback; raw provider errors are not printed.
Connecting CLI login/organization selection and distribution manifest loading to
this interface is not implemented. Until then, standalone binaries report the
default local context, not the channel of an adjacent `release.json`.

This read-only metadata interface does not change the providers used by operational
commands. It neither grants entitlements nor bypasses authorization or LTS download
eligibility. Software identity remains linker metadata with the existing numeric
release version contract; `1.0.0-enterprise` is rejected. Protocol version is a
separate constant, currently 1. Edition configuration and channel never modify the
software version or select different verification code. No migration is required.
