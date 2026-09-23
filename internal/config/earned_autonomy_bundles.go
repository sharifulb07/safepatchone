package config

import (
	_ "embed"
	"safepatch/internal/domain/feature"
)

//go:embed earned_autonomy_bundles.json
var earnedAutonomyBundles []byte

// EarnedAutonomyEditions is explicit provisioning input. Existing organization
// assignments remain pinned until an authoritative subscription/license change.
func EarnedAutonomyEditions(features *feature.FeatureCatalog) (EditionMatrix, error) {
	base, err := SafeUpgradeEditions(features)
	if err != nil {
		return EditionMatrix{}, err
	}
	return applyEditionUpdatesTo(features, earnedAutonomyBundles, base)
}
