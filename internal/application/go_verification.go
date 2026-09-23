package application

import (
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/verification"
)

// GoVerificationRequirement binds technical work to existing catalog entries.
// It grants nothing. Compose it with ExecutionGuard/WorkerOperation so current
// entitlement, independent actor permission and atomic quota admission precede
// the SDK. Verifiers receive no commercial configuration.
func GoVerificationRequirement(catalog *feature.FeatureCatalog, capability verification.Capability) (Requirement, error) {
	if catalog == nil {
		return Requirement{}, ErrInvalidGuard
	}
	key := feature.KeyVerifyBasic
	switch capability {
	case "go.build", "go.test", "go.test.race", "go.vet", "go.fuzz", "go.benchmark":
	case "go.staticcheck", "go.govulncheck":
		key = feature.KeyVerifySecurity
	default:
		return Requirement{}, ErrInvalidGuard
	}
	if _, err := catalog.Get(key); err != nil {
		return Requirement{}, err
	}
	return Requirement{Feature: key, Permission: authorization.KeyProofExecute}, nil
}
