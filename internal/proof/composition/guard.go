package composition

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
)

// Attestation is supplied by authoritative completed verification, not a model
// or uploaded status. Policy and manifest bindings prohibit subset proof reuse.
// SubjectID is a connection ID, or the manifest ID for the combined proof.
type Attestation struct {
	SubjectID      string          `json:"subject_id"`
	ManifestDigest string          `json:"manifest_digest"`
	PolicyDigest   string          `json:"policy_digest"`
	EvidenceDigest string          `json:"evidence_digest"`
	Status         evidence.Status `json:"status"`
	IssuedAt       time.Time       `json:"issued_at"`
	ValidUntil     time.Time       `json:"valid_until"`
}
type ComponentProof struct {
	ComponentID string
	Passport    passport.Passport
	ValidUntil  time.Time
}
type Input struct {
	Components  []ComponentProof
	Connections []Attestation
	Combined    *Attestation
}
type Check struct {
	Kind           string          `json:"kind"`
	ID             string          `json:"id"`
	Status         evidence.Status `json:"status"`
	Reason         string          `json:"reason"`
	EvidenceDigest string          `json:"evidence_digest"`
}
type ResultFields struct {
	Schema         string         `json:"schema"`
	OrganizationID string         `json:"organization_id"`
	ManifestDigest string         `json:"manifest_digest"`
	EvaluatedAt    time.Time      `json:"evaluated_at"`
	ValidUntil     time.Time      `json:"valid_until"`
	Decision       trust.Decision `json:"decision"`
	Checks         []Check        `json:"checks"`
}
type Result struct{ canonical string }

// CompositionGuard consumes results of the existing verification/policy core.
// It is not another verifier, and PASS never directly authorizes production.
type CompositionGuard struct{}

func (CompositionGuard) Evaluate(ctx context.Context, m Manifest, input Input, now time.Time) (Result, error) {
	bad := Result{}
	if ctx == nil {
		return bad, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	f, err := m.Fields()
	if err != nil || !validTime(now) || len(input.Components) > MaxComponents || len(input.Connections) > MaxConnections {
		return bad, ErrInvalid
	}
	now = now.UTC()
	components := map[string]Component{}
	edges := map[string]bool{}
	for _, c := range f.Components {
		components[c.ID] = c
	}
	for _, c := range f.Connections {
		edges[c.ID] = true
	}
	proofs := map[string]ComponentProof{}
	for _, p := range input.Components {
		if _, ok := components[p.ComponentID]; !ok {
			return bad, ErrInvalid
		}
		if _, ok := proofs[p.ComponentID]; ok {
			return bad, ErrInvalid
		}
		proofs[p.ComponentID] = p
	}
	contracts := map[string]Attestation{}
	for _, a := range input.Connections {
		if !edges[a.SubjectID] {
			return bad, ErrInvalid
		}
		if _, ok := contracts[a.SubjectID]; ok {
			return bad, ErrInvalid
		}
		contracts[a.SubjectID] = a
	}
	r := ResultFields{Schema: ResultSchema, OrganizationID: f.OrganizationID, ManifestDigest: m.Digest(), EvaluatedAt: now, ValidUntil: now.Add(time.Minute), Checks: []Check{}}
	statuses := []evidence.Status{}
	add := func(c Check) { r.Checks = append(r.Checks, c); statuses = append(statuses, c.Status) }
	expiry := func(t time.Time) {
		if t.Before(r.ValidUntil) {
			r.ValidUntil = t.UTC()
		}
	}
	for _, c := range f.Components {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		check := Check{Kind: "component", ID: c.ID, Status: evidence.StatusUnknown, Reason: "missing_proof"}
		p, ok := proofs[c.ID]
		if ok {
			fields, err := p.Passport.Fields()
			if err != nil || fields.Change.OrganizationID != f.OrganizationID || fields.Change.RepositoryID != c.RepositoryID || fields.Commit != c.Commit || p.Passport.Digest() != c.PassportDigest {
				return bad, ErrInvalid
			}
			check.EvidenceDigest = c.PassportDigest
			if !validTime(p.ValidUntil) || !p.ValidUntil.After(fields.IssuedAt) || fields.IssuedAt.After(now) {
				return bad, ErrInvalid
			}
			if !now.Before(p.ValidUntil) {
				check.Reason = "expired_proof"
			} else {
				expiry(p.ValidUntil)
				check.Status = evidence.Status(trust.Decide(fields.ProofLevel != passport.UnknownLevel, evidence.Status(fields.VerificationDecision), fields.PolicyDecision.Status))
				check.Reason = "component_verification"
			}
		}
		add(check)
	}
	checkAttestation := func(kind, id string, a *Attestation) (Check, error) {
		check := Check{Kind: kind, ID: id, Status: evidence.StatusUnknown, Reason: "missing_evidence"}
		if a == nil {
			return check, nil
		}
		if a.SubjectID != id || a.ManifestDigest != m.Digest() || a.PolicyDigest != f.PolicyDigest || !passport.ValidDigest(a.EvidenceDigest) || !validTime(a.IssuedAt) || !validTime(a.ValidUntil) || !a.ValidUntil.After(a.IssuedAt) || a.IssuedAt.After(now) {
			return check, ErrInvalid
		}
		switch a.Status {
		case evidence.StatusPass, evidence.StatusFail, evidence.StatusUnknown:
		default:
			return check, ErrInvalid
		}
		check.EvidenceDigest = a.EvidenceDigest
		if !now.Before(a.ValidUntil) {
			check.Reason = "expired_evidence"
			return check, nil
		}
		expiry(a.ValidUntil)
		check.Status = a.Status
		check.Reason = "verification_result"
		return check, nil
	}
	for _, edge := range f.Connections {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		var a *Attestation
		if value, ok := contracts[edge.ID]; ok {
			a = &value
		}
		check, err := checkAttestation("connection", edge.ID, a)
		if err != nil {
			return bad, err
		}
		add(check)
	}
	check, err := checkAttestation("combined", f.ID, input.Combined)
	if err != nil {
		return bad, err
	}
	add(check)
	r.Decision = trust.Decide(true, statuses...)
	slices.SortFunc(r.Checks, func(a, b Check) int { return strings.Compare(a.Kind+":"+a.ID, b.Kind+":"+b.ID) })
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxBytes {
		return bad, ErrInvalid
	}
	return Result{string(raw)}, nil
}
func (r Result) Fields() (ResultFields, error) {
	var f ResultFields
	if r.canonical == "" || json.Unmarshal([]byte(r.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (r Result) MarshalJSON() ([]byte, error) {
	if r.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(r.canonical), nil
}
func (r Result) Digest() string {
	if r.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(r.canonical))
}
func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
