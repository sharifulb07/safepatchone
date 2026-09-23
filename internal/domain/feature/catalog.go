package feature

import (
	"fmt"
	"slices"
)

// FeatureCatalog is an immutable, concurrency-safe metadata snapshot. Construct
// it with NewFeatureCatalog. Its zero value is an empty catalog. Lookups return
// copies and never confer entitlement, permission, or implementation readiness.
type FeatureCatalog struct {
	byKey map[Key]Feature
	byID  map[string]Key
	keys  []Key
}

func NewFeatureCatalog(features []Feature) (*FeatureCatalog, error) {
	if len(features) > MaxFeatures {
		return nil, fmt.Errorf("%w: too many features", ErrInvalidFeature)
	}
	c := &FeatureCatalog{byKey: make(map[Key]Feature, len(features)), byID: make(map[string]Key, len(features))}
	for _, f := range features {
		if err := f.Validate(); err != nil {
			return nil, err
		}
		if _, exists := c.byKey[f.Key]; exists {
			return nil, fmt.Errorf("%w: key %s", ErrDuplicateFeature, f.Key)
		}
		if _, exists := c.byID[f.FeatureID]; exists {
			return nil, fmt.Errorf("%w: feature_id %s", ErrDuplicateFeature, f.FeatureID)
		}
		f = cloneFeature(f)
		f.CreatedAt = f.CreatedAt.UTC()
		slices.Sort(f.RequiresFeatures)
		c.byKey[f.Key], c.byID[f.FeatureID] = f, f.Key
		c.keys = append(c.keys, f.Key)
	}
	slices.Sort(c.keys)
	if err := c.validateDependencies(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *FeatureCatalog) Get(key Key) (Feature, error) {
	f, exists := c.byKey[key]
	if !exists {
		return Feature{}, ErrFeatureNotFound
	}
	return cloneFeature(f), nil
}

func (c *FeatureCatalog) GetByID(id string) (Feature, error) {
	key, exists := c.byID[id]
	if !exists {
		return Feature{}, ErrFeatureNotFound
	}
	return c.Get(key)
}

// List returns snapshots sorted by key; requires_features is also sorted.
func (c *FeatureCatalog) List() []Feature {
	features := make([]Feature, 0, len(c.keys))
	for _, key := range c.keys {
		features = append(features, cloneFeature(c.byKey[key]))
	}
	return features
}

func (c *FeatureCatalog) Len() int { return len(c.keys) }

func cloneFeature(f Feature) Feature {
	f.RequiresFeatures = append([]Key{}, f.RequiresFeatures...)
	return f
}

func (c *FeatureCatalog) validateDependencies() error {
	remaining := make(map[Key]int, len(c.keys))
	dependents := make(map[Key][]Key, len(c.keys))
	var ready []Key
	for _, key := range c.keys {
		f := c.byKey[key]
		remaining[key] = len(f.RequiresFeatures)
		if remaining[key] == 0 {
			ready = append(ready, key)
		}
		for _, dependency := range f.RequiresFeatures {
			if _, exists := c.byKey[dependency]; !exists {
				return fmt.Errorf("%w: %s requires %s", ErrUnknownDependency, key, dependency)
			}
			dependents[dependency] = append(dependents[dependency], key)
		}
	}
	// Iterative topological traversal bounds work and avoids recursive stack use.
	for i := 0; i < len(ready); i++ {
		for _, key := range dependents[ready[i]] {
			remaining[key]--
			if remaining[key] == 0 {
				ready = append(ready, key)
			}
		}
	}
	if len(ready) != len(c.keys) {
		return ErrDependencyCycle
	}
	return nil
}
