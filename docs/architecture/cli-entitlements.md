# Entitlement-aware CLI

The shared CLI uses the existing offline `CommunityEntitlementProvider`; it does not read accounts, subscriptions, Stripe, repository configuration or online services. These commands inspect entitlement and local-session metadata:

```powershell
safepatch entitlement show
safepatch entitlement explain runtime.replay
safepatch entitlement explain verify.basic --json
safepatch edition
safepatch whoami --json
```

From source, replace `safepatch` with `go run ./cmd/safepatch`. For CI exit-code and JSON handling, invoke a built binary directly: `go run` may add its own `exit status` line to stderr. Build with `go build -o safepatch.exe ./cmd/safepatch` on Windows (use `-o safepatch` on Unix).

## Commands

| Command | Behavior |
| --- | --- |
| `entitlement show` | Shows the bundle and explicit enabled/disabled states for every known capability. Its existing JSON report remains compatible. |
| `entitlement explain <feature>` | Resolves one validated feature and reports state, scope, bundle version, source, feature limit, implementation status and resolver explanation. Disabled features return `FEATURE_NOT_ENTITLED` and exit 1. |
| `edition` | Shows Community and `community-2026-v2` as independent edition/bundle metadata. It does not infer a software version or release channel. |
| `whoami` | Reports a local offline session. JSON has `authenticated: false`, `actor: null`, and `organization: null`; the `local-cli` namespace is never presented as a real account. |

All support `--json`, including help. The flag may appear once anywhere in the argument list. Duplicate flags, unsupported flags, extra arguments and invalid feature-key syntax are usage errors. Help is available through `--help` or `-h`; for example, `safepatch entitlement explain --help`.

The Community text output includes:

```text
Edition:
Community
...
  proof.planner: disabled (implementation: PLANNED)
  runtime.replay: disabled (implementation: PLANNED)
  verify.basic: enabled (implementation: PLANNED)
```

Listing disabled features in `show` is a successful inspection. Requesting an explanation for a disabled feature is an explicit denied request and exits 1. An enabled explanation exits 0, which means only that entitlement inspection succeeded, not that verification ran or an actor has permission. The current execution engines remain unimplemented. `safepatch verify --json` returns `NOT_IMPLEMENTED` and exits 1.

## Stable errors and streams

JSON successes contain one JSON document on stdout and nothing on stderr. JSON failures leave stdout empty and emit one error envelope on stderr. Output write failures exit 1 and attempt an `OUTPUT_ERROR` envelope on stderr; if a writer partially writes before failing, stdout may contain an incomplete document and must be discarded. Failure of stderr itself still exits nonzero.

The error envelope has `error.code`, `error.message`, optional `error.feature`, and optional `error.entitlement`. For `entitlement explain runtime.replay --json`, the error code is `FEATURE_NOT_ENTITLED`, the feature is `runtime.replay`, and the nested entitlement state is `disabled`. That nested explanation retains bundle/source/implementation details so CI does not need to parse prose. Private underlying Go errors are never serialized.

| Code | Exit | Meaning |
| --- | --- | --- |
| `FEATURE_NOT_ENTITLED` | 1 | The registered feature is disabled in the effective bundle. |
| `UNKNOWN_FEATURE` | 2 | The key has valid syntax but is not registered. |
| `INVALID_USAGE` | 2 | Malformed key, invalid command/arguments, or duplicate `--json`. |
| `ENTITLEMENT_UNAVAILABLE` | 1 | Resolution, inspection, cancellation or consistent metadata is unavailable. No successful fallback. |
| `NOT_IMPLEMENTED` | 1 | The requested operational command is still unavailable. |
| `OUTPUT_ERROR` | 1 | Writing command output failed. |

Plain-text errors include the same stable code. Neither output mode includes sales pitches, upgrade links, ANSI escapes, interactive prompts or CI-specific advertising. No CI environment check is needed because output is non-promotional everywhere. Raw flags, invalid keys, credentials and provider error strings are not echoed into diagnostics.

## Boundaries and validation

CLI inspection requires no actor permission because it only displays built-in public configuration. It neither creates an authenticated identity nor changes the existing authenticate/tenant/entitlement/permission/quota/operation guard. Entitlement and authorization remain separate; no premium operation, cloud simulation, login flow or production authorization is introduced. There are no entitlement, database schema or migration changes in this milestone.

Tests cover enabled/disabled keys, exact JSON error codes, unknown/malformed input, consistency failures, cancellation, raw-error sanitization, output/short-write failures, help, and offline identity. Compiled-command tests run with `CI=true`, absent credentials, hostile local configuration and invalid proxies, checking actual exit codes and stdout/stderr separation. Existing entitlement, permission, quota, migration-history and edition-boundary tests continue to run in the full suite. Native PostgreSQL fixtures remain opt-in; this CLI milestone requires no database.

The current CLI composes only the local Community provider. Authenticated organization selection and remote identity are future milestones; `whoami` does not claim those integrations exist. Feature ownership remains independent of implementation status, and an LLM or successful inspection can never authorize production.
