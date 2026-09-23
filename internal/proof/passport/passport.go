// Package passport defines the portable, edition-independent SafePatch Passport.
// A valid signature authenticates a statement; it never authorizes deployment.
package passport

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/proof"
	"safepatch/internal/trust"
)

const Schema = "safepatch.passport.v1"
const MaxBytes = 16 * 1024
const UnknownLevel proof.Level = "UNKNOWN"

var ErrInvalid = errors.New("invalid SafePatch Passport")
var ErrSignature = errors.New("Passport signature is not trusted")

// Reference identifies immutable bytes in the subject's authorized artifact
// store. MediaType describes encoding only, not compliance with that standard.
// References are never URLs, filesystem paths, or instructions to fetch data.
type Reference struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
}

type PolicyDecision struct {
	PolicyDigest string          `json:"policy_digest"`
	ResultDigest string          `json:"result_digest"`
	Status       evidence.Status `json:"status"`
}

// Fields retains every key even when information is unavailable. A nil build
// artifact, SBOM, or provenance is explicitly unknown, not an empty attestation.
// ProofLevel is achieved observation coverage, never the planner's target alone.
type Fields struct {
	Change               domain.Change  `json:"change"`
	ChangeID             string         `json:"change_id"`
	RunID                string         `json:"run_id"`
	IntentDigest         string         `json:"intent_digest"`
	Commit               string         `json:"commit"`
	ArtifactDigest       *string        `json:"artifact_digest"`
	SBOM                 *Reference     `json:"sbom"`
	Provenance           *Reference     `json:"provenance"`
	ProofLevel           proof.Level    `json:"proof_level"`
	ProofPlanDigest      string         `json:"proof_plan_digest"`
	PolicyDecision       PolicyDecision `json:"policy_decision"`
	EvidenceGraphDigest  string         `json:"evidence_graph_digest"`
	VerificationDecision trust.Decision `json:"verification_decision"`
	IssuedAt             time.Time      `json:"issued_at"`
}

type Signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Value     []byte `json:"value"`
}

type document struct {
	Schema    string     `json:"schema"`
	Payload   Fields     `json:"payload"`
	Signature *Signature `json:"signature"`
}

// Passport owns its canonical encoding. Fields and Signature return copies.
type Passport struct{ canonical string }

func ValidDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validReference(r *Reference) bool {
	if r == nil {
		return true
	}
	if !ValidDigest(r.Digest) || len(r.MediaType) < 3 || len(r.MediaType) > 128 || strings.Count(r.MediaType, "/") != 1 {
		return false
	}
	parts := strings.Split(r.MediaType, "/")
	if parts[0] == "" || parts[1] == "" {
		return false
	}
	for _, c := range r.MediaType {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("/+.-", c)) {
			return false
		}
	}
	return true
}

func New(f Fields) (Passport, error) { return seal(document{Schema: Schema, Payload: f}) }

func seal(d document) (Passport, error) {
	f := &d.Payload
	if d.Schema != Schema || f.Change.Validate() != nil || f.ChangeID != f.Change.ID() || f.Commit != f.Change.HeadSHA || !evidence.ValidIdentifier(f.RunID) || f.IssuedAt.IsZero() || f.IssuedAt.UTC().Year() < 1 || f.IssuedAt.UTC().Year() > 9999 {
		return Passport{}, ErrInvalid
	}
	for _, digest := range []string{f.IntentDigest, f.ProofPlanDigest, f.PolicyDecision.PolicyDigest, f.PolicyDecision.ResultDigest, f.EvidenceGraphDigest} {
		if !ValidDigest(digest) {
			return Passport{}, ErrInvalid
		}
	}
	if f.ArtifactDigest != nil && !ValidDigest(*f.ArtifactDigest) || !validReference(f.SBOM) || !validReference(f.Provenance) || !f.PolicyDecision.Status.Valid() || f.ProofLevel != UnknownLevel && f.ProofLevel.Rank() < 0 {
		return Passport{}, ErrInvalid
	}
	switch f.VerificationDecision {
	case trust.Pass:
		if f.ProofLevel == UnknownLevel || f.PolicyDecision.Status != evidence.StatusPass {
			return Passport{}, ErrInvalid
		}
	case trust.Fail, trust.Unknown:
	default:
		return Passport{}, ErrInvalid
	}
	if d.Signature != nil && (d.Signature.Algorithm != "Ed25519" || !evidence.ValidIdentifier(d.Signature.KeyID) || len(d.Signature.Value) != ed25519.SignatureSize) {
		return Passport{}, ErrInvalid
	}
	f.IssuedAt = f.IssuedAt.UTC().Round(0)
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > MaxBytes {
		return Passport{}, ErrInvalid
	}
	return Passport{canonical: string(raw)}, nil
}

func (p Passport) read() (document, error) {
	var d document
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &d) != nil {
		return d, ErrInvalid
	}
	return d, nil
}
func (p Passport) Fields() (Fields, error)        { d, err := p.read(); return d.Payload, err }
func (p Passport) Signature() (*Signature, error) { d, err := p.read(); return d.Signature, err }
func (p Passport) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(p.canonical), nil
}
func (*Passport) UnmarshalJSON([]byte) error { return ErrInvalid }

// Digest identifies the entire canonical file, including its signature if any.
func (p Passport) Digest() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(p.canonical))
}

// Decode rejects unknown/missing fields, aliases, duplicates, null scalar fields
// and invalid nested values. Whitespace and object key ordering are immaterial.
// Decode does NOT establish authenticity; call Verify with an external trust key.
func Decode(raw []byte) (Passport, error) {
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return Passport{}, ErrInvalid
	}
	var d document
	if json.Unmarshal(checked, &d) != nil {
		return Passport{}, ErrInvalid
	}
	p, err := seal(d)
	if err != nil {
		return Passport{}, err
	}
	// Comparing normalized object trees enforces the precise published schema
	// without encoding/json's case-insensitive field matching or null coercion.
	var input, canonical any
	if json.Unmarshal(checked, &input) != nil || json.Unmarshal([]byte(p.canonical), &canonical) != nil {
		return Passport{}, ErrInvalid
	}
	a, _ := json.Marshal(input)
	b, _ := json.Marshal(canonical)
	if !bytes.Equal(a, b) {
		return Passport{}, ErrInvalid
	}
	return p, nil
}

func signingBytes(d document, keyID string) []byte {
	raw, _ := json.Marshal(d.Payload)
	return append([]byte(Schema+"\nEd25519\n"+keyID+"\n"), raw...)
}

// Sign uses operator-owned keys. It must never be exposed as a signing oracle
// for arbitrary uploaded claims. Hosted orchestration loads trusted run output.
func Sign(p Passport, keyID string, key ed25519.PrivateKey) (Passport, error) {
	d, err := p.read()
	if err != nil || d.Signature != nil || !evidence.ValidIdentifier(keyID) || len(key) != ed25519.PrivateKeySize {
		return Passport{}, ErrSignature
	}
	// Reject inconsistent private-key encodings instead of emitting bad signatures.
	if !bytes.Equal(ed25519.NewKeyFromSeed(key[:ed25519.SeedSize]), key) {
		return Passport{}, ErrSignature
	}
	d.Signature = &Signature{Algorithm: "Ed25519", KeyID: keyID, Value: ed25519.Sign(key, signingBytes(d, keyID))}
	return seal(d)
}

// Verify trusts only the caller's externally configured, organization-scoped key.
// No embedded public key, URL, or claim can add itself to the trust store.
func Verify(p Passport, organizationID, keyID string, key ed25519.PublicKey) error {
	d, err := p.read()
	if err != nil || d.Payload.Change.OrganizationID != organizationID || d.Signature == nil || d.Signature.KeyID != keyID || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, signingBytes(d, keyID), d.Signature.Value) {
		return ErrSignature
	}
	return nil
}
