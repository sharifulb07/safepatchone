package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"

	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/jsonobject"
)

// The same configuration is seeded by migration 000006; parity is tested.
// This is provisioning input, never an effective grant or assignment lookup.
//
//go:embed editions_v02.json
var editionsV02 []byte

type EditionConfiguration struct {
	Bundle edition.Bundle            `json:"bundle"`
	Limits []limits.EntitlementLimit `json:"limits"`
}
type EditionMatrix struct {
	Release            string                 `json:"release"`
	LimitProfileStatus string                 `json:"limit_profile_status"`
	Editions           []EditionConfiguration `json:"editions"`
}

var ErrInvalidEditionMatrix = errors.New("invalid edition matrix configuration")

// V02Editions returns owned configuration validated by the existing catalog and
// resource-limit model. No wall clock, network, billing or customer writes occur.
// A trusted provisioner explicitly pins bundle versions and applies policies.
func V02Editions(features *feature.FeatureCatalog) (EditionMatrix, error) {
	return decodeEditionMatrix(editionsV02, features)
}
func decodeEditionMatrix(raw []byte, features *feature.FeatureCatalog) (EditionMatrix, error) {
	bad := EditionMatrix{}
	checked, err := jsonobject.Normalize(raw, 1024*1024)
	if err != nil {
		return bad, ErrInvalidEditionMatrix
	}
	var m EditionMatrix
	d := json.NewDecoder(bytes.NewReader(checked))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || m.Release != "0.2.0" || m.LimitProfileStatus != "PROVISIONAL" || len(m.Editions) != len(edition.Editions()) {
		return bad, ErrInvalidEditionMatrix
	}
	catalog, err := edition.NewCatalog(features)
	if err != nil {
		return bad, err
	}
	seen := map[edition.Key]bool{}
	for i, entry := range m.Editions {
		if seen[entry.Bundle.Version.EditionKey] {
			return bad, ErrInvalidEditionMatrix
		}
		seen[entry.Bundle.Version.EditionKey] = true
		catalog, err = catalog.Publish(entry.Bundle.Version, entry.Bundle.Features)
		if err != nil {
			return bad, err
		}
		m.Editions[i].Bundle, err = catalog.GetVersion(entry.Bundle.Version.EditionVersionID)
		if err != nil {
			return bad, err
		}
		for _, mapping := range entry.Bundle.Features {
			var metadata struct {
				Release      string `json:"release"`
				Availability string `json:"availability"`
				Scope        string `json:"scope,omitempty"`
			}
			decoder := json.NewDecoder(bytes.NewReader(mapping.ConfigurationJSON))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&metadata) != nil || metadata.Release != m.Release || (metadata.Availability != "SDK" && metadata.Availability != "FUTURE") || (metadata.Scope != "" && (metadata.Scope != "foundations" || metadata.Availability != "FUTURE")) {
				return bad, ErrInvalidEditionMatrix
			}
		}
		// This sealed v0.2 profile has eight historical resource identities. New
		// resource keys default disabled in authoritative storage, not retrofitted
		// into immutable published edition bundles.
		if len(entry.Limits) != 8 {
			return bad, ErrInvalidEditionMatrix
		}
		keys := map[limits.LimitKey]bool{}
		for _, limit := range entry.Limits {
			if limit.Key == limits.LimitDatasetRetentionDays || limit.Key == limits.LimitReplayConcurrency {
				return bad, ErrInvalidEditionMatrix
			}
			if limit.Validate() != nil || keys[limit.Key] {
				return bad, ErrInvalidEditionMatrix
			}
			keys[limit.Key] = true
		}
	}
	return m, nil
}
