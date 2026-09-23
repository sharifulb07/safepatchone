package entitlement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

const MaxSnapshotTTL = time.Minute

var (
	ErrInvalidSnapshot     = errors.New("invalid entitlement snapshot")
	ErrExpiredSnapshot     = errors.New("entitlement snapshot is outside its validity window")
	snapshotEditionVersion = regexp.MustCompile(`^([a-z]+)-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$`)
)

// SourceVersions are authoritative monotonic revisions, including for sources
// with no grants. Writers advance the corresponding revision atomically with
// every change. Revisions must never be reused or reset (including after restore).
type SourceVersions struct {
	Subscription         uint64 `json:"subscription"`
	Trial                uint64 `json:"trial"`
	License              uint64 `json:"license"`
	ManualOverride       uint64 `json:"manual_override"`
	EditionConfiguration uint64 `json:"edition_configuration"`
	CommunityDefault     uint64 `json:"community_default"`
	Promotion            uint64 `json:"promotion"`
}

// SnapshotState identifies the organization assignment and all source revisions.
// Edition fields are metadata only; they never generate feature grants.
type SnapshotState struct {
	OrganizationID string         `json:"organization_id"`
	Edition        edition.Key    `json:"edition"`
	EditionVersion string         `json:"edition_version"`
	SourceVersions SourceVersions `json:"source_versions"`
}

func (s SnapshotState) Validate() error {
	if !ValidID(s.OrganizationID) {
		return ErrInvalidSnapshot
	}
	if _, err := edition.Lookup(s.Edition); err != nil {
		return ErrInvalidSnapshot
	}
	if len(s.EditionVersion) > 64 {
		return ErrInvalidSnapshot
	}
	parts := snapshotEditionVersion.FindStringSubmatch(s.EditionVersion)
	if len(parts) != 2 || parts[1] != string(s.Edition) {
		return ErrInvalidSnapshot
	}
	v := s.SourceVersions
	if v.Subscription == 0 || v.Trial == 0 || v.License == 0 || v.ManualOverride == 0 || v.EditionConfiguration == 0 || v.CommunityDefault == 0 || v.Promotion == 0 {
		return ErrInvalidSnapshot
	}
	return nil
}

// EffectiveEntitlementSnapshot is derived data, not a bearer authorization token.
// A content hash detects corruption; it is not a signature or proof of freshness.
// Limits are feature-keyed because canonical entitlements currently have one
// limit per feature. Nil means unspecified, never unlimited.
type EffectiveEntitlementSnapshot struct {
	OrganizationID string                 `json:"organization_id"`
	Edition        edition.Key            `json:"edition"`
	EditionVersion string                 `json:"edition_version"`
	Features       map[feature.Key]bool   `json:"features"`
	Limits         map[feature.Key]*int64 `json:"limits"`
	GeneratedAt    time.Time              `json:"generated_at"`
	ValidUntil     time.Time              `json:"valid_until"`
	SourceVersions SourceVersions         `json:"source_versions"`
	ContentHash    string                 `json:"content_hash"`
}

func (s EffectiveEntitlementSnapshot) State() SnapshotState {
	return SnapshotState{s.OrganizationID, s.Edition, s.EditionVersion, s.SourceVersions}
}

func (s EffectiveEntitlementSnapshot) Clone() EffectiveEntitlementSnapshot {
	s.Features = maps.Clone(s.Features)
	s.Limits = maps.Clone(s.Limits)
	for key, value := range s.Limits {
		if value != nil {
			n := *value
			s.Limits[key] = &n
		}
	}
	return s
}

// ComputeHash hashes canonical JSON with sorted map keys, UTC timestamps, and
// content_hash set to the empty string. It does not validate or trust the input.
func (s EffectiveEntitlementSnapshot) ComputeHash() (string, error) {
	s.ContentHash = ""
	s.GeneratedAt, s.ValidUntil = s.GeneratedAt.UTC(), s.ValidUntil.UTC()
	data, err := json.Marshal(s)
	if err != nil {
		return "", ErrInvalidSnapshot
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateAt checks structure, tenant, integrity, and [generated_at, valid_until).
// The service must ALSO check current authoritative SourceVersions before use.
func (s EffectiveEntitlementSnapshot) ValidateAt(organizationID string, at time.Time) error {
	if s.OrganizationID != organizationID || s.State().Validate() != nil || s.GeneratedAt.IsZero() || s.ValidUntil.IsZero() || !s.ValidUntil.After(s.GeneratedAt) || s.ValidUntil.Sub(s.GeneratedAt) > MaxSnapshotTTL {
		return ErrInvalidSnapshot
	}
	if len(s.Features) == 0 || len(s.Features) > feature.MaxFeatures || len(s.Limits) != len(s.Features) {
		return ErrInvalidSnapshot
	}
	for key, enabled := range s.Features {
		limit, exists := s.Limits[key]
		if feature.ValidateKey(key) != nil || !exists || (limit != nil && *limit < 0) || (!enabled && (limit == nil || *limit != 0)) {
			return ErrInvalidSnapshot
		}
	}
	hash, err := s.ComputeHash()
	if err != nil || hash != s.ContentHash {
		return ErrInvalidSnapshot
	}
	if at.IsZero() || at.Before(s.GeneratedAt) || !at.Before(s.ValidUntil) {
		return ErrExpiredSnapshot
	}
	return nil
}
