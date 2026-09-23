# Deployment configuration

The [downloads deployment guide](../docs/architecture/download-distribution.md)
uses `downloads.example.json` and `downloads.nginx.conf` for public release assets
and protected, short-lived S3 downloads on downloads.safepatch.dev.

The [shared release pipeline](../docs/releases.md) packages all six commands and
uses `Dockerfile.release` for every container role. Images contain the same Linux
binaries as release ZIPs and resolve edition capabilities at runtime. Several
entrypoints remain command shells; see the release guide before deployment.

Deployment configuration for shared process roles belongs here. PostgreSQL, NATS JetStream, S3-compatible storage, and OpenTelemetry adapters are future work. Configuration and licensing select capabilities; edition-specific source copies or builds are forbidden.
