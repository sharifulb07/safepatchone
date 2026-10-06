# Deployment configuration

The [downloads deployment guide](../docs/architecture/download-distribution.md)
uses `downloads.example.json` and `downloads.nginx.conf` for public release assets
and protected, short-lived S3 downloads on downloads.safepatch.dev.

The [shared release pipeline](../docs/releases.md) packages all six commands and
uses `Dockerfile.release` for every container role. Images contain the same Linux
binaries as release ZIPs and resolve edition capabilities at runtime. Several
entrypoints remain command shells; see the release guide before deployment.

Deployment configuration for shared process roles belongs here. PostgreSQL, NATS JetStream and S3 adapters exist, but live infrastructure acceptance and the verification worker composition remain release gates. Follow the [current review](../docs/releases/2026-10-03-review.md), [onboarding guide](../docs/architecture/onboarding.md), and [Paddle guide](../docs/architecture/paddle-setup.md) before configuring the account service. Configuration and licensing select capabilities; edition-specific source copies or builds are forbidden.
