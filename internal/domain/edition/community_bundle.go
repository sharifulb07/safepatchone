package edition

import (
	"time"

	"safepatch/internal/domain/feature"
)

// CommunityBundleVersion is commercial configuration, never a software version
// or release channel. Published bundles must not be edited in place.
const CommunityBundleVersion = "community-2026-v2"

func CommunityBundleRegisteredAt() time.Time {
	return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
}

// NewCommunityBundle materializes the built-in local defaults through the same
// immutable edition validators used for every offering. SQL/Go parity and
// preservation of v1 are enforced by migration tests. Local evidence is additive;
// the four previously published Community capabilities remain granted.
func NewCommunityBundle(features *feature.FeatureCatalog) (Bundle, error) {
	catalog, err := NewCatalog(features)
	if err != nil {
		return Bundle{}, err
	}
	version := EditionVersion{EditionVersionID: CommunityBundleVersion, EditionKey: Community, CreatedAt: CommunityBundleRegisteredAt()}
	keys := []feature.Key{feature.KeyVerifyBasic, feature.KeyVerifySecurity, feature.KeyIntentBasic, feature.KeyGraphLocal, feature.KeyEvidenceLocal}
	mappings := make([]EditionFeature, 0, len(keys))
	for _, key := range keys {
		definition, err := features.Get(key)
		if err != nil {
			return Bundle{}, err
		}
		mappings = append(mappings, EditionFeature{EditionVersionID: version.EditionVersionID, FeatureID: definition.FeatureID, Enabled: true})
	}
	catalog, err = catalog.Publish(version, mappings)
	if err != nil {
		return Bundle{}, err
	}
	return catalog.GetVersion(version.EditionVersionID)
}
