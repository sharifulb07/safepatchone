# downloads.safepatch.dev

Release distribution uses the existing Go API, PostgreSQL-backed session and
organization membership, authoritative organization entitlements, and permission
service. There is no separate Enterprise application or subscription database.
Enable it with `SAFEPATCH_DOWNLOADS_CONFIG` pointing to a trusted catalog file;
`safepatch-api serve` registers the routes alongside existing services.

| Artifact classification | Access |
| --- | --- |
| Unscoped stable `cli` / `public_asset` | Anonymous public download |
| `self_host_bundle` | `enterprise.self_hosted` |
| `airgap_bundle` | `enterprise.self_hosted` AND `enterprise.airgap` |
| Any `lts` artifact | Additionally `enterprise.lts` |
| Other nonpublic CLI/release assets | `verify.basic` |

Every protected request also requires current organization membership and
`organization.manage`. An optional catalog `organization_id` restricts an artifact
to that organization even when another organization has every required feature.
Commercial edition names never participate in these checks. An LTS artifact can
never become public by using kind `public_asset`; public status requires stable,
an eligible public kind and no organization binding.

## HTTP flow

- `GET /v1/downloads/public` lists only unexpired public metadata.
- `GET /v1/downloads/public/{artifact}` returns an anonymous short-lived URL for
  an explicitly public artifact. Unknown, expired and protected IDs return 404.
- `POST /v1/organizations/{organization}/downloads/{artifact}` authenticates the
  existing opaque session bearer, resolves the organization, checks every required
  entitlement, checks actor permission and artifact organization binding, then
  signs the download. It accepts no body or query parameters.

Authentication reuses `Sessions.Authenticate`. Send a SafePatch session through
`Authorization: Bearer ...`, supplied securely by the existing login integration;
GitHub access tokens, Stripe credentials, cookies and client-supplied identity
claims are not accepted. Browser integrations should call through their trusted
authenticated backend; this milestone does not add cross-domain cookie sharing,
session export, a new login system or a download dashboard. Duplicate Authorization
headers, malformed IDs and query/body storage overrides are rejected.

A successful response contains `url`, `expires_at`, `filename`, `sha256` and
`bytes`. The URL permits GET on exactly the catalog object. It expires within
60 seconds of admission, or sooner at artifact/temporary-credential expiry.
Credential lookup time consumes that budget. Responses set private/no-store,
no-cache and no-referrer headers; the S3 response also enforces attachment download,
application/octet-stream and private/no-store. Signing runs only after admission.
The handler bounds concurrent issuance to 16 and each request to ten seconds;
the provided edge configuration applies an additional per-IP rate limit.

Signed URLs are bearer capabilities and can be reused until expiry. New requests
recheck downgrades and membership changes, but an issued URL is not retrospectively
revoked by a plan change. No permanent premium URL is returned: an unsigned object
URL must be denied by private bucket policy. Do not log response bodies, signatures
or Authorization headers, or cache the issuance response. The nginx access format
records the URI path rather than query strings.

## Storage and publication

The official AWS SDK presigns against a fixed HTTPS S3-compatible endpoint and
bucket, using the standard credential provider chain. Production should use a
short-lived workload role restricted to `s3:GetObject` and, if used,
`s3:GetObjectVersion` under the release prefix. No credential is placed in the
catalog, binary or example configuration. The private secret key is never returned;
the signed query necessarily contains credential identifiers and any session token.

Object keys are constructed as `releases/<sha256>/<filename>`, with a validated
filename and digest. Clients cannot supply keys, buckets, redirect destinations
or content headers. Optional `object_version` pins a stored version and is included
in the signature. Keep publication writes separate from the read-only signing role;
use object versioning/immutable retention or an enforced write-once publication
process. The loader rejects public/protected aliases sharing the same digest.
Treat catalog edits as privileged publication changes, not user uploads.

Adapt [downloads.example.json](../../deploy/downloads.example.json): its sample
digests, byte counts and filenames are placeholders, not published artifacts.
For every asset, verify the shared release signature/checksum first, calculate its
actual SHA-256 and length, upload those exact bytes at the constructed key, then
register its immutable catalog entry with an availability deadline and classification.
Use the same shared executables in self-host and air-gap packaging; do not include
customer credentials or private license-signing keys. This milestone distributes
provided bundles and does not create new bundle contents.

All objects, including public download objects, may remain in a private bucket:
the anonymous route signs only explicitly public entries. On AWS, enable all S3
Block Public Access settings and deny non-TLS reads; a bucket policy limiting
`s3:signatureAge` to 60000 ms adds an independent upper bound. Verify equivalent
controls with the chosen compatible provider. Never place protected assets on
the public GitHub-release/registry origin. The source configuration file is bounded
to 2 MiB, strict JSON with duplicate/unknown/case-aliased fields rejected, and at
most 4096 validated records. No customer can mutate it through the API.

## Deployment

1. Provision the private bucket/read role and populate verified immutable objects.
2. Configure the existing API's database, NATS, authentication and billing runtime,
   then set `SAFEPATCH_DOWNLOADS_CONFIG` to the adapted catalog. AWS credentials come
   from the standard provider chain; configure the endpoint/region in the catalog.
3. Point `downloads.safepatch.dev` to the HTTPS edge and provision its TLS certificate.
   Review and install [downloads.nginx.conf](../../deploy/downloads.nginx.conf),
   adjusting the backend address as needed. Expose only its listed download routes.
   Keep the API origin private and disable CDN caching of signed-link responses.
4. Check anonymous CLI access, protected-route denial without authentication, each
   required entitlement and independent permission, unsigned object denial and
   expired signed URL denial before enabling public traffic.

These files are deployment configuration, not a claim that DNS, certificates,
bucket IAM or the live domain have been changed. The public catalog is a JSON
index; the edge root redirects there. Existing API startup infrastructure remains
required. No new migration, release stream or entitlement key is introduced.

Implementation references: [AWS Go SDK presigning](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/go_s3_code_examples.html),
[presigned URL lifetime and bearer behavior](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html),
and [signature-age guardrails](https://docs.aws.amazon.com/prescriptive-guidance/latest/presigned-url-best-practices/overview.html).

## Task report

Inspected the existing release/channel/artifact services, PlatformAccess and
ExecutionGuard, organization permissions, session authentication, onboarding API
composition, S3 evidence adapter, shared API specification, Go module and release
documentation. Reused their identity, entitlement and authorization implementations.

Created `internal/domain/release/artifact.go`, `internal/application/downloads.go`,
`internal/platform/downloads/{config.go,http.go,s3.go,downloads_test.go,security_test.go}`,
the two deployment examples and this document. Modified
`internal/platform/onboardinghost/server.go`, `go.mod`, `go.sum`,
`api/openapi/openapi.yaml`, `docs/releases.md`, `deploy/README.md` and
`docs/architecture/release-channels.md`.
AWS SDK infrastructure remains outside domain/application code. No SQL changes.

Tests use actual HTTP handlers and the actual SDK presigner with fixture credentials.
They independently recompute SigV4, exercise all five editions for each protected
feature combination, and verify entitlement/permission/tenant denial, downgrade and
restoration, immutable catalog data, public/protected separation, organization-bound
artifacts, duplicate auth, traversal/URL/header overrides, credential/artifact expiry,
configuration ambiguity and cancellation. They do not contact a real bucket or
publish the live domain. Native PostgreSQL migration execution retains its opt-in gate.

Validation on 2026-09-07: `go test ./...`, `go test -race ./...` and
`go vet ./...` passed, including architecture checks. Native PostgreSQL tests
explicitly skipped without their database opt-in. nginx, Docker and the AWS CLI
were not installed locally; the edge configuration was not executed, and real
S3 bucket policy/expiry behavior and DNS/TLS remain unverified. No credentials,
artifacts or signatures were published to a live service.

Next recommended prompt: provision the private release bucket and downloads HTTPS
edge, stage verified artifacts, then verify real signed URL expiry and unsigned
object denial against the configured S3 provider.
