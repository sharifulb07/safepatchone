// Package feature defines the shared product capability catalog. Catalog
// membership is neither an organization entitlement nor an actor permission.
package feature

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Key string
type Category string
type Status string
type LimitType string

const (
	CategoryVerify     Category = "VERIFY"
	CategoryIntent     Category = "INTENT"
	CategoryGraph      Category = "GRAPH"
	CategoryProof      Category = "PROOF"
	CategoryRuntime    Category = "RUNTIME"
	CategoryAgent      Category = "AGENT"
	CategorySecurity   Category = "SECURITY"
	CategoryEnterprise Category = "ENTERPRISE"
	CategoryPlatform   Category = "PLATFORM"

	StatusPlanned    Status = "PLANNED"
	StatusActive     Status = "ACTIVE"
	StatusDeprecated Status = "DEPRECATED"

	// LimitNone means no default metering type is specified, not unlimited access.
	LimitNone     LimitType = "NONE"
	LimitCount    LimitType = "COUNT"
	LimitDuration LimitType = "DURATION"
	LimitBytes    LimitType = "BYTES"
	LimitCredits  LimitType = "CREDITS"

	MaxFeatures     = 4096
	MaxDependencies = 64
)

var (
	ErrInvalidFeature    = errors.New("invalid feature")
	ErrDuplicateFeature  = errors.New("duplicate feature identity")
	ErrUnknownDependency = errors.New("unknown feature dependency")
	ErrDependencyCycle   = errors.New("feature dependency cycle")
	ErrFeatureNotFound   = errors.New("feature not found")
	keyPattern           = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*(\.[a-z][a-z0-9]*(_[a-z0-9]+)*)*$`)
	idPattern            = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
)

// Feature is a value snapshot. FeatureID and Key are permanent identities and
// must never be recycled. DeprecatedVersion is empty until deprecation.
type Feature struct {
	FeatureID         string    `json:"feature_id"`
	Key               Key       `json:"key"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	Category          Category  `json:"category"`
	Status            Status    `json:"status"`
	IntroducedVersion string    `json:"introduced_version"`
	DeprecatedVersion string    `json:"deprecated_version"`
	DefaultLimitType  LimitType `json:"default_limit_type"`
	RequiresFeatures  []Key     `json:"requires_features"`
	CreatedAt         time.Time `json:"created_at"`
}

// ValidateKey rejects edition tokens, except the exact capability identifiers
// explicitly required by the product contract. Those exceptions do not allow
// arbitrary new enterprise.* or business.* keys.
func ValidateKey(key Key) error {
	s := string(key)
	if len(s) == 0 || len(s) > 128 || !keyPattern.MatchString(s) {
		return fmt.Errorf("%w: key must be lowercase ASCII segments (maximum 128 bytes)", ErrInvalidFeature)
	}
	switch key {
	case KeyBusinessInvariants, KeyEnterpriseSSO, KeyEnterpriseAudit,
		KeyEnterpriseSelfHosted, KeyEnterpriseAirgap, KeyEnterpriseLTS:
		return nil
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '.' || r == '_' }) {
		switch part {
		case "community", "developer", "team", "business", "enterprise":
			return fmt.Errorf("%w: key contains an edition token", ErrInvalidFeature)
		}
	}
	return nil
}

func (f Feature) Validate() error {
	invalid := func(field string) error { return fmt.Errorf("%w: %s", ErrInvalidFeature, field) }
	if len(f.FeatureID) == 0 || len(f.FeatureID) > 128 || !idPattern.MatchString(f.FeatureID) {
		return invalid("feature_id must be a bounded ASCII identifier")
	}
	if err := ValidateKey(f.Key); err != nil {
		return err
	}
	if !validText(f.Name, 128, false) {
		return invalid("name")
	}
	if !validText(f.Description, 4096, true) {
		return invalid("description")
	}
	switch f.Category {
	case CategoryVerify, CategoryIntent, CategoryGraph, CategoryProof, CategoryRuntime,
		CategoryAgent, CategorySecurity, CategoryEnterprise, CategoryPlatform:
	default:
		return invalid("category")
	}
	switch f.Status {
	case StatusPlanned, StatusActive, StatusDeprecated:
	default:
		return invalid("status")
	}
	switch f.DefaultLimitType {
	case LimitNone, LimitCount, LimitDuration, LimitBytes, LimitCredits:
	default:
		return invalid("default_limit_type")
	}
	introduced, ok := parseVersion(f.IntroducedVersion)
	if !ok {
		return invalid("introduced_version must be a semantic version")
	}
	if f.Status == StatusDeprecated {
		deprecated, ok := parseVersion(f.DeprecatedVersion)
		if !ok || compareVersion(deprecated, introduced) < 0 {
			return invalid("deprecated_version must be at or after introduced_version")
		}
	} else if f.DeprecatedVersion != "" {
		return invalid("deprecated_version requires DEPRECATED status")
	}
	if f.CreatedAt.IsZero() || f.CreatedAt.UTC().Year() < 1 || f.CreatedAt.UTC().Year() > 9999 {
		return invalid("created_at must be a nonzero JSON-compatible timestamp")
	}
	if len(f.RequiresFeatures) > MaxDependencies {
		return invalid("too many requires_features")
	}
	seen := make(map[Key]bool, len(f.RequiresFeatures))
	for _, dependency := range f.RequiresFeatures {
		if err := ValidateKey(dependency); err != nil {
			return invalid("requires_features contains an invalid key")
		}
		if dependency == f.Key || seen[dependency] {
			return invalid("requires_features contains self or duplicate dependency")
		}
		seen[dependency] = true
	}
	return nil
}

func validText(value string, max int, multiline bool) bool {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) {
			return false
		}
	}
	return true
}
