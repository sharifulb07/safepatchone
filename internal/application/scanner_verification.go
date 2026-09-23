package application

import (
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/verification"
)

type ScannerLocation string

const (
	ScannerLocal  ScannerLocation = "LOCAL"
	ScannerHosted ScannerLocation = "HOSTED"
)

// ScannerVerificationRequirement must be selected by trusted composition. A
// server always binds HOSTED; request JSON/CLI flags cannot downgrade admission.
// Both locations use the existing security-verification grant (already present
// in Community). Hosted compute requires a real tenant's authoritative grants,
// independent proof.execute permission and the existing guard's quota admission.
// The offline provider is never valid for a hosted tenant. Execution mode is separate from location:
// untrusted local sources also need Sandbox, and hosted sources always do.
func ScannerVerificationRequirement(catalog *feature.FeatureCatalog, capability verification.Capability, location ScannerLocation) (Requirement, error) {
	if catalog == nil {
		return Requirement{}, ErrInvalidGuard
	}
	switch capability {
	case "scanner.gitleaks", "scanner.semgrep", "scanner.trivy", "scanner.osv", "scanner.syft":
	default:
		return Requirement{}, ErrInvalidGuard
	}
	key := feature.KeyVerifySecurity
	switch location {
	case ScannerLocal, ScannerHosted:
	default:
		return Requirement{}, ErrInvalidGuard
	}
	if _, err := catalog.Get(key); err != nil {
		return Requirement{}, err
	}
	return Requirement{Feature: key, Permission: authorization.KeyProofExecute}, nil
}
