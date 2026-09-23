# Software version, commercial edition, and release channel

Status: proposed product model. Examples are illustrative; they do not announce a released version, support commitment, entitlement allocation, or implemented capability.

**Software Version != Commercial Edition != Release Channel.**

| Dimension | Meaning | Example values | Must not determine |
| --- | --- | --- | --- |
| Software version | Identity of software behavior, API compatibility, and migration compatibility | `1.5.2` | Organization purchases or actor permissions |
| Commercial edition | One of five permanent offerings, resolved through approved grants and limits | Community, Developer, Team, Business, Enterprise | A separate codebase, API, schema, engine, or migration tree |
| Release channel | Update/support track selecting compatible releases from the shared release stream | `stable`, `LTS` | Feature ownership, actor permission, or an edition fork |

## Edition/version matrix

The same software version supports every edition through configuration, entitlements, limits, policy, and licensing:

| Edition | Illustrative software version | Illustrative channel | Capability source | Schema/migrations |
| --- | --- | --- | --- | --- |
| Community | `1.5.2` | `stable` | Approved catalog and effective grants/limits | Shared |
| Developer | `1.5.2` | `stable` | Approved catalog and effective grants/limits | Shared |
| Team | `1.5.2` | `stable` | Approved catalog and effective grants/limits | Shared |
| Business | `1.5.2` | `stable` | Approved catalog and effective grants/limits | Shared |
| Enterprise | `1.5.2` | `LTS` | Approved catalog and effective grants/limits | Shared |

The last row illustrates independence; it does not reserve LTS for Enterprise. Team on LTS or Enterprise on stable must not require a different domain architecture. Availability and support terms for channels remain a commercial decision, separate from runtime feature checks.

Track version/build metadata with the running software, edition/grant revisions with the organization/license, and channel with deployment update configuration. A multi-tenant deployment can serve organizations with different editions on the same version and channel. Effective grants may also reflect approved contract overrides; callers still check capability keys.

## Release and data lifecycle

- Maintain one release stream, shared API specification, shared schema, and one ordered migration history. Supported platform/process artifacts may differ by operating system, architecture, or role, never by commercial domain implementation.
- Channels select/promote releases from that stream. Record compatibility and support metadata explicitly. LTS does not authorize an edition-specific code or migration fork; any future backport process must preserve shared implementation and migration lineage.
- A feature is usable only when implemented in the running version and allowed by entitlements, permissions, limits, and policy. Owning a grant for a future capability cannot make an older binary implement it. Unknown capabilities fail closed.
- Edition changes update grants without a binary replacement or edition-specific migration. Preserve data, evidence, and references on downgrade; test historical access policy independently.
- Software upgrades apply shared migrations regardless of tenant editions. Define supported upgrade paths and test mixed-edition datasets.
- Channel changes may select a different version, but do not implicitly downgrade a database. Validate binary/schema compatibility, backup and restore procedures, and migration reversibility before permitting version rollback. Entitlement downgrade and software rollback are different operations.

## Bounded roadmap

These are recommended future milestones, not work implemented by this audit:

The subsequent [structural scaffold](../architecture/repository-structure.md) implements the requested directory boundaries, shared command shell, and architecture tests. It is only part of the foundation: tenant/access contracts, release metadata, and an independent repository boundary remain future work.

| Sequence | Milestone | Acceptance evidence |
| --- | --- | --- |
| 0 | Current repository audit and architecture/threat documentation | Honest inventory, explicit gaps, and four linked documents; no feature implementation |
| 1 | Shared Go foundation and repository boundary | Minimal composition root, independent metadata types, tenant/entitlement/permission contracts, explicit UNKNOWN semantics, focused and race tests |
| 2 | Tenant-safe persistence and migration foundation | One schema/migration chain, ownership constraints, cross-tenant negative tests, and migration fixtures spanning all five editions |
| 3 | Effective grants, limits, and license lifecycle | Approved catalog allocation, fail-closed errors, atomic quota accounting, revocation, expiry, and upgrade/downgrade data-preservation tests |
| 4 | Shared verification and durable evidence workflow | One engine, scoped work admission/consumption, immutable validated evidence, bounded execution, and no UNKNOWN-to-PASS path |
| 5 | Incremental premium capabilities and channel operations | Per-feature access boundaries, data-preservation tests, shared release artifacts, compatibility evidence, and documented channel policy |

For each premium capability, require proof that an entitled organization can access it, a non-entitled organization cannot, actor authorization still applies after entitlement passes, and plan changes do not corrupt existing data. Run `go test ./...` and `go test -race ./...` after meaningful implementation changes, alongside the appropriate integration, security, error-path, and migration tests.

No feature quotas, release dates, support durations, licensing grace periods, or paid feature assignments are invented here. Those require explicit decisions before their respective milestones.

See [current state](../architecture/current-state.md), [one-codebase architecture](../architecture/one-codebase-editions.md), and [threat model](../security/threat-model.md).
