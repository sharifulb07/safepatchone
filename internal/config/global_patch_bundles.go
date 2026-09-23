package config

import (
	_ "embed"
	"safepatch/internal/domain/feature"
)

//go:embed global_patch_bundles.json
var globalPatchBundles []byte

// GlobalPatchEditions extends the last immutable provisioning profile. Existing
// subscriptions remain pinned; no runtime operation branches on edition.
func GlobalPatchEditions(features *feature.FeatureCatalog) (EditionMatrix, error) {
	base, err := CompositionEditions(features)
	if err != nil {
		return EditionMatrix{}, err
	}
	return applyEditionUpdatesTo(features, globalPatchBundles, base)
}
