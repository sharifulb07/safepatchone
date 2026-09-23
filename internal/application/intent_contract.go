package application

import (
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
)

// IntentRequirement selects an operation's admission requirement at trusted
// composition, never from repository text, LLM output or an edition string.
// Use with ExecutionGuard/WorkerOperation. SpecService binds its implemented
// operations to these requirements; all operations share IntentContract.
func IntentRequirement(catalog *feature.FeatureCatalog, capability feature.Key) (Requirement, error) {
	if catalog == nil {
		return Requirement{}, ErrInvalidGuard
	}
	switch capability {
	case feature.KeyIntentBasic, feature.KeyIntentAdvanced, feature.KeyIntentSpecforge:
	default:
		return Requirement{}, ErrInvalidGuard
	}
	if _, err := catalog.Get(capability); err != nil {
		return Requirement{}, err
	}
	return Requirement{Feature: capability, Permission: authorization.KeyProofExecute}, nil
}
