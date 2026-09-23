package config

import (
	_ "embed"
	"safepatch/internal/domain/feature"
)

//go:embed safe_upgrade_bundles.json
var safeUpgradeBundles []byte

// SafeUpgradeEditions is explicit provisioning input, not a runtime grant.
func SafeUpgradeEditions(features *feature.FeatureCatalog) (EditionMatrix, error) {
	base, err := GlobalPatchEditions(features)
	if err != nil {
		return EditionMatrix{}, err
	}
	return applyEditionUpdatesTo(features, safeUpgradeBundles, base)
}
