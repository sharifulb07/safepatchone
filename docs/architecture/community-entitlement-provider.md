# Offline Community entitlements

`CommunityEntitlementProvider` implements the existing `EntitlementService` interface over a compiled, immutable Community bundle. The CLI inspects it entirely locally:

```powershell
go run ./cmd/safepatch entitlement show
go run ./cmd/safepatch entitlement show --json
```

With an installed binary, use `safepatch entitlement show`. There is no account creation, login, subscription, Stripe call, license file, database, network lookup or configuration download. Inspection creates no files. It ignores environment variables and repository configuration, including attempted edition overrides. `--json` lists all known capabilities with independent `entitled` and `implementation_status` fields; human output lists explicit enabled/disabled states. Invalid arguments exit 2, output failures exit 1, and successful inspection exits 0. The [entitlement-aware CLI](cli-entitlements.md) also supplies `entitlement explain <feature>`, `edition`, `whoami`, and stable machine-readable errors.

## Versioned built-in policy

The bundle is `community-2026-v2`, registered at `2026-09-06T00:00:00Z`. It preserves the four grants from `community-2026-v1` and adds a dedicated local evidence identity:

| Key | Local entitlement |
| --- | --- |
| `verify.basic` | Basic verification |
| `verify.security` | Previously granted security verification; preserved from v1 |
| `intent.basic` | Basic IntentGuard |
| `graph.local` | Basic local ChangeGraph |
| `evidence.local` | Local evidence; permanent ID `feat_0039` |

`evidence.local` does not imply `proof.cloud_history`, `proof.shared` or `proof.passport`. All omitted capabilities deny, including cloud history, private GitHub integration, organization/cross-repository graphs, replay, shadow execution and enterprise deployment capabilities. Offline local CLI access does not require or grant `enterprise.airgap` or `enterprise.self_hosted`, which describe commercial platform deployment capabilities.

The bundle is configuration within the shared edition domain, not a separate Community application. Product software version and release channel remain independent. Tests construct the same bundle using multiple software catalog versions. Changing the compiled bundle requires a new immutable commercial revision and an additive migration; never edit an old published revision. Higher editions pinned to their existing v1 bundles remain unchanged by this migration; upgrading their mappings is a separate policy publication.

Migration `000004_community_local_bundle.up.sql` registers `evidence.local` and publishes v2 using the existing immutable publisher. It does not update any old bundle, assign/move customers, reset quotas or create another schema. A parity test compares the compiled bundle to the exact migration JSON through the shared domain validators, and tests preserve every previous edition mapping. The CLI does not read or execute SQL at runtime.

## Resolution and scope

```go
provider, err := entitlement.NewCommunityEntitlementProvider()
owned, err := provider.Has(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic)
limit, err := provider.Limit(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic)
resolution, err := provider.Resolve(ctx, entitlement.LocalOrganizationID, feature.KeyVerifyBasic)
bundle, err := provider.Bundle(ctx)
report, err := provider.Inspect(ctx)
```

Construction validates the built-in catalog and bundle, converts mappings into canonical `COMMUNITY_DEFAULT` entitlement records, and reuses the existing resolver and its frozen record adapter. Inspection returns owned copies. No mutable global state, paid-provider dependencies, or edition-name business branches are added.

The fixed `local-cli` scope is an in-process namespace for the organization-shaped service contract. It is not an account or proof of membership in a hosted organization. Every hosted or malformed organization ID is rejected. Do not use this provider as a fallback when a remote entitlement service fails, and never register it as a hosted tenant provider. Server and worker shells do not route to the local inspection command.

These compiled defaults are perpetual and have no expiration. Their resolver evaluates immutable records at the fixed bundle registration instant rather than treating the machine clock as an offline lease. `Resolution.Explanation` therefore identifies that fixed evaluation instant; it is not a claim of current remote freshness. Expiring licenses, trials, mutable restrictions, remote snapshots and revocation remain the responsibility of their existing services. The provider does not accept those records.

Feature limits remain unspecified. `Limit` returns `ErrLimitUnspecified` for granted features and `ErrNotEntitled` for denied features; null does not become unlimited. No resource quota numbers are invented and the previous quota service is unchanged. Operational composition must provide explicit quota policy before using metered workflows.

## Authorization and implementation status

Public bundle inspection needs no actor permission because it only prints built-in metadata. Performing work still requires independent authorization and policy checks. The provider does not authenticate an actor, claim filesystem ownership, register memberships, authorize production, or bypass `PermissionService.Allowed` after entitlement succeeds. Integration tests use trusted local identity fixtures and the real permission service/guard to demonstrate entitled access, permission denial after entitlement, and cloud capability denial even for an owner. These fixtures are not production authentication adapters.

All catalog capabilities remain `PLANNED`. This milestone grants local capabilities and implements offline entitlement inspection; it does not implement verification, evidence storage, IntentGuard, ChangeGraph, production dispatch or cloud substitutes. `safepatch verify` continues to exit 1 with `not implemented yet`; it cannot report PASS. A successful entitlement inspection is never a verification outcome. Future local engines must use these same capability contracts and implement their own trusted resource/permission boundaries.

## Validation

Tests cover exact grants and denials across the entire catalog, unchanged limit semantics, nil/unconfigured providers, cancellation, hostile keys/scope, concurrent readers, defensive copying, software/bundle version independence, actor permissions and migration history preservation. The compiled CLI runs in a temporary working directory with no inherited account credentials, nonexistent home/profile directories, invalid network proxies and hostile repository/edition inputs. The provider's dependency closure is checked for network, database, billing and licensing packages; future online commands may still share the CLI. This verifies the offline inspection path without claiming a firewall-level network isolation test.

Native PostgreSQL migration assertions use the shared opt-in harness described in [migrations](../../migrations/README.md). PGlite can exercise the same SQL assertions without a server, but does not prove native multi-connection PostgreSQL behavior.
