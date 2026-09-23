// Package entitlement defines provider-independent organization grant records.
// A record is not an actor permission or a resolved multi-source access decision.
package entitlement

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/jsonobject"
)

type Source string

const (
	SourceCommunityDefault  Source = "COMMUNITY_DEFAULT"
	SourceSubscription      Source = "SUBSCRIPTION"
	SourceTrial             Source = "TRIAL"
	SourceEnterpriseLicense Source = "ENTERPRISE_LICENSE"
	SourcePromotion         Source = "PROMOTION"
	SourceManualOverride    Source = "MANUAL_OVERRIDE"
	MaxMetadataBytes               = 16384
)

var (
	ErrInvalidEntitlement = errors.New("invalid entitlement")
	idPattern             = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
	referencePattern      = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,255}$`)
)

// Entitlement is a canonical value snapshot. Null LimitValue means unspecified,
// never unlimited. Null ExpiresAt means no scheduled expiration. Metadata is
// inert data and cannot change Enabled, dates, limits, or source authority.
type Entitlement struct {
	EntitlementID   string          `json:"entitlement_id"`
	OrganizationID  string          `json:"organization_id"`
	FeatureID       string          `json:"feature_id"`
	Source          Source          `json:"source"`
	SourceReference string          `json:"source_reference"`
	Enabled         bool            `json:"enabled"`
	LimitValue      *int64          `json:"limit_value"`
	StartsAt        time.Time       `json:"starts_at"`
	ExpiresAt       *time.Time      `json:"expires_at"`
	Metadata        json.RawMessage `json:"metadata"`
	CreatedAt       time.Time       `json:"created_at"`
}

// NewEntitlement validates and copies a record, including checking the existing
// FeatureCatalog. It does not verify organization existence, issuer authority,
// license signatures, payment status, or permissions; those require adapters
// and application services. All timestamps are caller supplied and stored in UTC.
func NewEntitlement(input Entitlement, catalog *feature.FeatureCatalog) (Entitlement, error) {
	if err := input.Validate(); err != nil {
		return Entitlement{}, err
	}
	if catalog == nil {
		return Entitlement{}, invalid("feature catalog required")
	}
	if _, err := catalog.GetByID(input.FeatureID); err != nil {
		return Entitlement{}, invalid("unknown feature_id")
	}
	result := input.Clone()
	result.StartsAt = result.StartsAt.UTC()
	result.CreatedAt = result.CreatedAt.UTC()
	if result.ExpiresAt != nil {
		utc := result.ExpiresAt.UTC()
		result.ExpiresAt = &utc
	}
	metadata, err := jsonobject.Normalize(input.Metadata, MaxMetadataBytes)
	if err != nil {
		return Entitlement{}, invalid("metadata")
	}
	result.Metadata = metadata
	return result, nil
}

// Validate checks record structure; catalog and organization existence must
// also be checked before persistence. JSON decoding alone is not validation.
func (e Entitlement) Validate() error {
	if !ValidID(e.EntitlementID) {
		return invalid("entitlement_id")
	}
	if !ValidID(e.OrganizationID) {
		return invalid("organization_id")
	}
	if !ValidID(e.FeatureID) {
		return invalid("feature_id")
	}
	switch e.Source {
	case SourceCommunityDefault, SourceSubscription, SourceTrial, SourceEnterpriseLicense, SourcePromotion, SourceManualOverride:
	default:
		return invalid("source")
	}
	if len(e.SourceReference) > 256 || !referencePattern.MatchString(e.SourceReference) {
		return invalid("source_reference must be a bounded opaque record reference")
	}
	if e.LimitValue != nil && *e.LimitValue < 0 {
		return invalid("limit_value must be nonnegative or unspecified")
	}
	if !validTime(e.StartsAt) {
		return invalid("starts_at")
	}
	if !validTime(e.CreatedAt) {
		return invalid("created_at")
	}
	if e.ExpiresAt != nil && (!validTime(*e.ExpiresAt) || !e.ExpiresAt.After(e.StartsAt)) {
		return invalid("expires_at must be after starts_at")
	}
	if _, err := jsonobject.Normalize(e.Metadata, MaxMetadataBytes); err != nil {
		return invalid("metadata must be a bounded JSON object")
	}
	return nil
}

// ActiveAt reports only this record's enabled/time state: starts_at inclusive,
// expires_at exclusive. It does not merge sources, consume quota, check an
// actor's permissions, or assert that a PLANNED feature is executable.
// Invalid records and invalid evaluation times return false with an error.
func (e Entitlement) ActiveAt(at time.Time) (bool, error) {
	if err := e.Validate(); err != nil {
		return false, err
	}
	if !validTime(at) {
		return false, invalid("evaluation time")
	}
	return e.Enabled && !at.Before(e.StartsAt) && (e.ExpiresAt == nil || at.Before(*e.ExpiresAt)), nil
}

func (e Entitlement) Clone() Entitlement {
	e.Metadata = slices.Clone(e.Metadata)
	if e.LimitValue != nil {
		limit := *e.LimitValue
		e.LimitValue = &limit
	}
	if e.ExpiresAt != nil {
		expires := *e.ExpiresAt
		e.ExpiresAt = &expires
	}
	return e
}

// ValidID defines bounded opaque identity syntax; an ID is not a path, URL,
// credential, proof of tenant membership, or proof that a resource exists.
func ValidID(id string) bool { return len(id) > 0 && len(id) <= 128 && idPattern.MatchString(id) }

func validTime(at time.Time) bool {
	return !at.IsZero() && at.UTC().Year() >= 1 && at.UTC().Year() <= 9999
}
func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidEntitlement, field) }
