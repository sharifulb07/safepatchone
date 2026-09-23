package config

import (
	"safepatch/internal/application"
	"safepatch/internal/domain/feature"
)

// Immutable policy IDs are worker configuration, not repository supplied code.
func OnboardingPolicies() []application.OnboardingPolicy {
	return []application.OnboardingPolicy{
		{ID: "baseline-v1", Name: "Baseline verification", Description: "Run the configured baseline verifier checks. Missing evidence remains UNKNOWN.", Feature: feature.KeyPolicyLocalBasic},
		{ID: "strict-v1", Name: "Strict verification", Description: "Require the configured extended verifier checks in addition to the baseline.", Feature: feature.KeyPolicyLocalAdvanced},
	}
}
