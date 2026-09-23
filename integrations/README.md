# External integrations

External provider adapters belong here. They depend inward on shared contracts; core domain and application packages must not import them.

`scanners` integrates installed Gitleaks, Semgrep, Trivy, OSV-Scanner and Syft through the shared Verifier SDK and Runner, with immutable original evidence and normalized reports. See [scanner integration and provisioning](../docs/architecture/scanner-integrations.md). Hosted runtime composition remains separate.
