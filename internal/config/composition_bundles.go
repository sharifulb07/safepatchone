package config

import (
	"bytes"
	_ "embed"
	"encoding/json"

	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/jsonobject"
)

//go:embed composition_bundles.json
var compositionBundles []byte

type editionUpdates struct {
	Schema  string          `json:"schema"`
	Updates []editionUpdate `json:"updates"`
}
type editionUpdate struct {
	BaseVersion string                    `json:"base_version"`
	Version     edition.EditionVersion    `json:"version"`
	Features    []edition.EditionFeature  `json:"features"`
	Limits      []limits.EntitlementLimit `json:"limits,omitempty"`
}

// CompositionEditions returns an additive provisioning profile. Existing customer
// assignments, offers, quotas and sealed V02Editions are never changed implicitly.
// Commercial reassignment is a new configuration row, not an engine branch.
func CompositionEditions(features *feature.FeatureCatalog) (EditionMatrix, error) {
	return applyEditionUpdates(features, compositionBundles)
}
func applyEditionUpdates(features *feature.FeatureCatalog, raw []byte) (EditionMatrix, error) {
	m, err := V02Editions(features)
	if err != nil {
		return EditionMatrix{}, err
	}
	return applyEditionUpdatesTo(features, raw, m)
}
func applyEditionUpdatesTo(features *feature.FeatureCatalog, raw []byte, m EditionMatrix) (EditionMatrix, error) {
	// The input profile remains immutable even when another profile extends it.
	m.Editions = append([]EditionConfiguration{}, m.Editions...)
	bad := EditionMatrix{}
	checked, err := jsonobject.Normalize(raw, 64*1024)
	if err != nil {
		return bad, ErrInvalidEditionMatrix
	}
	var changes editionUpdates
	decoder := json.NewDecoder(bytes.NewReader(checked))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&changes) != nil || changes.Schema != "safepatch.edition-updates.v1" || len(changes.Updates) == 0 || len(changes.Updates) > 5 {
		return bad, ErrInvalidEditionMatrix
	}
	catalog, _ := edition.NewCatalog(features)
	for _, entry := range m.Editions {
		catalog, err = catalog.Publish(entry.Bundle.Version, entry.Bundle.Features)
		if err != nil {
			return bad, err
		}
	}
	seen := map[edition.Key]bool{}
	for _, update := range changes.Updates {
		if seen[update.Version.EditionKey] || len(update.Features) == 0 {
			return bad, ErrInvalidEditionMatrix
		}
		seen[update.Version.EditionKey] = true
		base, err := catalog.GetVersion(update.BaseVersion)
		if err != nil || base.Version.EditionKey != update.Version.EditionKey || !update.Version.CreatedAt.After(base.Version.CreatedAt) {
			return bad, ErrInvalidEditionMatrix
		}
		mappings := map[string]edition.EditionFeature{}
		for _, mapping := range base.Features {
			mapping.EditionVersionID = update.Version.EditionVersionID
			mappings[mapping.FeatureID] = mapping
		}
		changed := map[string]bool{}
		for _, mapping := range update.Features {
			if changed[mapping.FeatureID] || mapping.EditionVersionID != update.Version.EditionVersionID {
				return bad, ErrInvalidEditionMatrix
			}
			changed[mapping.FeatureID] = true
			mappings[mapping.FeatureID] = mapping
		}
		rows := make([]edition.EditionFeature, 0, len(mappings))
		for _, mapping := range mappings {
			rows = append(rows, mapping)
		}
		catalog, err = catalog.Publish(update.Version, rows)
		if err != nil {
			return bad, err
		}
		bundle, _ := catalog.GetVersion(update.Version.EditionVersionID)
		seenLimits := map[limits.LimitKey]bool{}
		for _, limit := range update.Limits {
			if limit.Validate() != nil || seenLimits[limit.Key] {
				return bad, ErrInvalidEditionMatrix
			}
			seenLimits[limit.Key] = true
		}
		for i := range m.Editions {
			if m.Editions[i].Bundle.Version.EditionKey == update.Version.EditionKey {
				m.Editions[i].Bundle = bundle
				m.Editions[i].Limits = append([]limits.EntitlementLimit{}, m.Editions[i].Limits...)
				for _, limit := range update.Limits {
					found := false
					for j := range m.Editions[i].Limits {
						if m.Editions[i].Limits[j].Key == limit.Key {
							m.Editions[i].Limits[j] = limit
							found = true
						}
					}
					if !found {
						m.Editions[i].Limits = append(m.Editions[i].Limits, limit)
					}
				}
			}
		}
	}
	return m, nil
}
