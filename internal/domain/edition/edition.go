// Package edition describes commercial metadata and immutable feature bundles.
// Edition names never grant entitlements or authorize application behavior.
package edition

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"safepatch/internal/domain/feature"
)

type Key string

const (
	Community                   Key = "community"
	Developer                   Key = "developer"
	Team                        Key = "team"
	Business                    Key = "business"
	Enterprise                  Key = "enterprise"
	MaxVersions                     = 4096
	MaxConfigurationBytes           = 16384
	MaxBundleConfigurationBytes     = 1048576
)

var (
	ErrInvalidEdition  = errors.New("invalid edition metadata")
	ErrVersionExists   = errors.New("edition version already exists")
	ErrVersionNotFound = errors.New("edition version not found")
	ErrMappingNotFound = errors.New("edition feature mapping not found")
	versionPattern     = regexp.MustCompile(`^([a-z]+)-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$`)
)

type Edition struct {
	Key  Key    `json:"key"`
	Name string `json:"name"`
}

// Editions returns exactly the five permanent offerings in commercial order.
func Editions() []Edition {
	return []Edition{{Community, "Community"}, {Developer, "Developer"}, {Team, "Team"}, {Business, "Business"}, {Enterprise, "Enterprise"}}
}

func Lookup(key Key) (Edition, error) {
	for _, edition := range Editions() {
		if edition.Key == key {
			return edition, nil
		}
	}
	return Edition{}, ErrInvalidEdition
}

type EditionVersion struct {
	EditionVersionID string    `json:"edition_version_id"`
	EditionKey       Key       `json:"edition_key"`
	CreatedAt        time.Time `json:"created_at"`
}

// EditionFeature is bundle metadata, not an effective access decision.
// Nil LimitValue means unspecified; zero is an explicit zero, never unlimited.
// ConfigurationJSON is a bounded JSON object copied at every catalog boundary.
type EditionFeature struct {
	EditionVersionID  string          `json:"edition_version_id"`
	FeatureID         string          `json:"feature_id"`
	Enabled           bool            `json:"enabled"`
	LimitValue        *int64          `json:"limit_value"`
	ConfigurationJSON json.RawMessage `json:"configuration_json"`
}

type Bundle struct {
	Version  EditionVersion   `json:"version"`
	Features []EditionFeature `json:"features"`
}

// Catalog is an immutable history. Publish returns a new snapshot; it cannot
// replace an existing version, even with identical content. Customers must pin
// EditionVersionID rather than select a latest version by edition key.
type Catalog struct {
	features *feature.FeatureCatalog
	bundles  map[string]Bundle
}

func NewCatalog(features *feature.FeatureCatalog) (*Catalog, error) {
	if features == nil {
		return nil, fmt.Errorf("%w: feature catalog required", ErrInvalidEdition)
	}
	return &Catalog{features: features, bundles: make(map[string]Bundle)}, nil
}

func (c *Catalog) Publish(version EditionVersion, mappings []EditionFeature) (*Catalog, error) {
	if c == nil || c.features == nil {
		return nil, fmt.Errorf("%w: initialize catalog first", ErrInvalidEdition)
	}
	if _, exists := c.bundles[version.EditionVersionID]; exists {
		return nil, ErrVersionExists
	}
	if len(c.bundles) >= MaxVersions {
		return nil, fmt.Errorf("%w: too many versions", ErrInvalidEdition)
	}
	bundle, err := c.validateBundle(version, mappings)
	if err != nil {
		return nil, err
	}
	next := &Catalog{features: c.features, bundles: make(map[string]Bundle, len(c.bundles)+1)}
	// Existing bundles are private and immutable, so snapshots can share them.
	for id, prior := range c.bundles {
		next.bundles[id] = prior
	}
	next.bundles[version.EditionVersionID] = bundle
	return next, nil
}

func (c *Catalog) GetVersion(id string) (Bundle, error) {
	if c == nil {
		return Bundle{}, ErrVersionNotFound
	}
	bundle, exists := c.bundles[id]
	if !exists {
		return Bundle{}, ErrVersionNotFound
	}
	return cloneBundle(bundle), nil
}

func (c *Catalog) GetFeature(versionID, featureID string) (EditionFeature, error) {
	bundle, err := c.GetVersion(versionID)
	if err != nil {
		return EditionFeature{}, err
	}
	for _, mapping := range bundle.Features {
		if mapping.FeatureID == featureID {
			return mapping, nil
		}
	}
	return EditionFeature{}, ErrMappingNotFound
}

func (c *Catalog) Versions(key Key) ([]EditionVersion, error) {
	if _, err := Lookup(key); err != nil {
		return nil, err
	}
	result := []EditionVersion{}
	if c != nil {
		for _, bundle := range c.bundles {
			if bundle.Version.EditionKey == key {
				result = append(result, bundle.Version)
			}
		}
	}
	slices.SortFunc(result, func(a, b EditionVersion) int {
		if a.EditionVersionID < b.EditionVersionID {
			return -1
		}
		if a.EditionVersionID > b.EditionVersionID {
			return 1
		}
		return 0
	})
	return result, nil
}

func (c *Catalog) validateBundle(v EditionVersion, mappings []EditionFeature) (Bundle, error) {
	invalid := func(reason string) (Bundle, error) { return Bundle{}, fmt.Errorf("%w: %s", ErrInvalidEdition, reason) }
	if _, err := Lookup(v.EditionKey); err != nil {
		return invalid("unknown edition key")
	}
	if len(v.EditionVersionID) > 64 {
		return invalid("version ID too long")
	}
	parts := versionPattern.FindStringSubmatch(v.EditionVersionID)
	if len(parts) != 2 || parts[1] != string(v.EditionKey) {
		return invalid("version ID must match edition-YYYY-vN")
	}
	if v.CreatedAt.IsZero() || v.CreatedAt.UTC().Year() < 1 || v.CreatedAt.UTC().Year() > 9999 {
		return invalid("created_at")
	}
	if len(mappings) > feature.MaxFeatures {
		return invalid("too many mappings")
	}
	v.CreatedAt = v.CreatedAt.UTC()
	result := Bundle{Version: v, Features: make([]EditionFeature, 0, len(mappings))}
	seen := make(map[string]bool, len(mappings))
	enabled := make(map[feature.Key]bool, len(mappings))
	definitions := make(map[string]feature.Feature, len(mappings))
	total := 0
	for _, m := range mappings {
		if m.EditionVersionID != v.EditionVersionID {
			return invalid("mapping belongs to another version")
		}
		definition, err := c.features.GetByID(m.FeatureID)
		if err != nil {
			return invalid("unknown feature_id")
		}
		if seen[m.FeatureID] {
			return invalid("duplicate feature_id")
		}
		seen[m.FeatureID] = true
		if m.LimitValue != nil && *m.LimitValue < 0 {
			return invalid("negative limit_value")
		}
		configuration, err := normalizeConfiguration(m.ConfigurationJSON)
		if err != nil {
			return invalid("configuration_json must be a bounded, unambiguous JSON object")
		}
		total += len(configuration)
		if total > MaxBundleConfigurationBytes {
			return invalid("bundle configuration too large")
		}
		m.ConfigurationJSON = configuration
		m = cloneMapping(m)
		result.Features = append(result.Features, m)
		enabled[definition.Key] = m.Enabled
		definitions[m.FeatureID] = definition
	}
	for _, m := range result.Features {
		if !m.Enabled {
			continue
		}
		for _, dependency := range definitions[m.FeatureID].RequiresFeatures {
			if !enabled[dependency] {
				return invalid("enabled feature requires an enabled prerequisite")
			}
		}
	}
	slices.SortFunc(result.Features, func(a, b EditionFeature) int {
		if a.FeatureID < b.FeatureID {
			return -1
		}
		if a.FeatureID > b.FeatureID {
			return 1
		}
		return 0
	})
	return result, nil
}

func cloneMapping(m EditionFeature) EditionFeature {
	m.ConfigurationJSON = append(json.RawMessage{}, m.ConfigurationJSON...)
	if m.LimitValue != nil {
		value := *m.LimitValue
		m.LimitValue = &value
	}
	return m
}

func cloneBundle(b Bundle) Bundle {
	features := make([]EditionFeature, 0, len(b.Features))
	for _, m := range b.Features {
		features = append(features, cloneMapping(m))
	}
	b.Features = features
	return b
}
