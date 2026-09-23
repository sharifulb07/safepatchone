package licensing

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const MaxLicenseBytes = 256 * 1024
const MaxLicenseFeatures = 512

var ErrInvalidLicense = errors.New("invalid or inactive signed license")
var ErrLicenseUnavailable = errors.New("license source unavailable")

type FeatureGrant struct {
	Key        feature.Key `json:"key"`
	Enabled    bool        `json:"enabled"`
	LimitValue *int64      `json:"limit_value"`
}
type LicenseDocument struct {
	Schema           string                    `json:"schema"`
	LicenseID        string                    `json:"license_id"`
	OrganizationID   string                    `json:"organization_id"`
	Edition          edition.Key               `json:"edition"`
	EditionVersionID string                    `json:"edition_version_id"`
	Features         []FeatureGrant            `json:"features"`
	Limits           []limits.EntitlementLimit `json:"limits"`
	IssuedAt         time.Time                 `json:"issued_at"`
	ExpiresAt        time.Time                 `json:"expires_at"`
}
type SignedLicense struct {
	Schema    string          `json:"schema"`
	Algorithm string          `json:"algorithm"`
	KeyID     string          `json:"key_id"`
	Document  json.RawMessage `json:"document"`
	Signature string          `json:"signature"`
}

// VerifiedLicense owns immutable bytes. It is not an authorization credential or
// a freshness cache: the provider revalidates source state/time on every call.
type VerifiedLicense struct {
	canonical    string
	keyID        string
	availability LicenseAvailability
}

func (v VerifiedLicense) Document() (LicenseDocument, error) {
	var d LicenseDocument
	if v.canonical == "" || json.Unmarshal([]byte(v.canonical), &d) != nil {
		return d, ErrInvalidLicense
	}
	return d, nil
}
func (v VerifiedLicense) Digest() string {
	if v.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(v.canonical))
}
func (v VerifiedLicense) KeyID() string { return v.keyID }
func canonical(raw []byte) ([]byte, error) {
	clean, err := jsonobject.Normalize(raw, MaxLicenseBytes)
	if err != nil {
		return nil, ErrInvalidLicense
	}
	dec := json.NewDecoder(bytes.NewReader(clean))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return nil, ErrInvalidLicense
	}
	return json.Marshal(v)
}
func strict(raw []byte, target any) error {
	clean, err := canonical(raw)
	if err != nil || json.Unmarshal(clean, target) != nil {
		return ErrInvalidLicense
	}
	typed, err := json.Marshal(target)
	if err != nil {
		return ErrInvalidLicense
	}
	expected, err := canonical(typed)
	if err != nil || !bytes.Equal(clean, expected) {
		return ErrInvalidLicense
	}
	return nil
}

// SigningBytes defines the public issuer interoperability contract. This node
// package does not sign or accept a private key. Sign these bytes externally
// using standard Ed25519, then base64-encode the signature in SignedLicense.
func SigningBytes(keyID string, document []byte) ([]byte, error) {
	if !limits.ValidID(keyID) {
		return nil, ErrInvalidLicense
	}
	var d LicenseDocument
	if strict(document, &d) != nil {
		return nil, ErrInvalidLicense
	}
	raw, err := canonical(document)
	if err != nil {
		return nil, err
	}
	return append([]byte("safepatch.signed-license.v1\nEd25519\n"+keyID+"\n"), raw...), nil
}
func validateDocument(d LicenseDocument, catalog *feature.FeatureCatalog, editions *edition.Catalog) error {
	if d.Schema != "safepatch.license.v1" || !limits.ValidID(d.LicenseID) || !limits.ValidID(d.OrganizationID) || d.Features == nil || d.Limits == nil || len(d.Features) > MaxLicenseFeatures || len(d.Limits) > len(limits.LimitKeys()) || d.IssuedAt.IsZero() || d.IssuedAt.Year() < 1 || d.ExpiresAt.Year() > 9999 || !d.ExpiresAt.After(d.IssuedAt) || d.IssuedAt.Nanosecond() != 0 || d.ExpiresAt.Nanosecond() != 0 {
		return ErrInvalidLicense
	}
	bundle, err := editions.GetVersion(d.EditionVersionID)
	if err != nil || bundle.Version.EditionKey != d.Edition {
		return ErrInvalidLicense
	}
	seen := map[feature.Key]bool{}
	for _, g := range d.Features {
		if seen[g.Key] || g.LimitValue != nil && *g.LimitValue < 0 {
			return ErrInvalidLicense
		}
		if _, err := catalog.Get(g.Key); err != nil {
			return ErrInvalidLicense
		}
		seen[g.Key] = true
	}
	resourceKeys := map[limits.LimitKey]bool{}
	for _, l := range d.Limits {
		if l.Validate() != nil || resourceKeys[l.Key] {
			return ErrInvalidLicense
		}
		resourceKeys[l.Key] = true
	}
	return nil
}
func verify(raw []byte, keys map[string]ed25519.PublicKey, catalog *feature.FeatureCatalog, editions *edition.Catalog, org string, now time.Time) (VerifiedLicense, error) {
	var envelope SignedLicense
	if len(raw) == 0 || len(raw) > MaxLicenseBytes || strict(raw, &envelope) != nil || envelope.Schema != "safepatch.signed-license.v1" || envelope.Algorithm != "Ed25519" {
		return VerifiedLicense{}, ErrInvalidLicense
	}
	key := keys[envelope.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return VerifiedLicense{}, ErrInvalidLicense
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize || base64.StdEncoding.EncodeToString(signature) != envelope.Signature {
		return VerifiedLicense{}, ErrInvalidLicense
	}
	message, err := SigningBytes(envelope.KeyID, envelope.Document)
	if err != nil || !ed25519.Verify(key, message, signature) {
		return VerifiedLicense{}, ErrInvalidLicense
	}
	var d LicenseDocument
	if strict(envelope.Document, &d) != nil || validateDocument(d, catalog, editions) != nil || d.OrganizationID != org || now.Before(d.IssuedAt) || !now.Before(d.ExpiresAt) {
		return VerifiedLicense{}, ErrInvalidLicense
	}
	// Canonical owned inspection bytes; ordering never changes signature input.
	d.Features = slices.Clone(d.Features)
	sort.Slice(d.Features, func(i, j int) bool { return d.Features[i].Key < d.Features[j].Key })
	d.Limits = slices.Clone(d.Limits)
	sort.Slice(d.Limits, func(i, j int) bool { return d.Limits[i].Key < d.Limits[j].Key })
	rawDocument, _ := json.Marshal(d)
	return VerifiedLicense{canonical: string(rawDocument), keyID: envelope.KeyID}, nil
}
