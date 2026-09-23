package agent

import (
	"context"
	"encoding/json"
	"safepatch/internal/authorization"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/trust"
	"slices"
	"strings"
	"time"
)

// AutonomyPolicy is operator-reviewed maximum authority, not generated intent.
// Existing Rule validation excludes production/unknown effects and wildcards.
type AutonomyPolicy struct {
	ID                 string      `json:"id"`
	Policy             Policy      `json:"policy"`
	MinimumProofs      uint32      `json:"minimum_proofs"`
	MinimumLevel       proof.Level `json:"minimum_level"`
	MaxProofAgeSeconds uint32      `json:"max_proof_age_seconds"`
}

func (p AutonomyPolicy) Digest() string {
	if !evidence.ValidIdentifier(p.ID) || p.Policy.Digest() == "" || p.MinimumProofs == 0 || p.MinimumProofs > 32 || p.MinimumLevel.Rank() < 0 || p.MaxProofAgeSeconds == 0 || p.MaxProofAgeSeconds > 86400 {
		return ""
	}
	// Canonical policy digest makes rule order irrelevant.
	raw, _ := json.Marshal(struct {
		ID, Policy    string
		MinimumProofs uint32
		MinimumLevel  proof.Level
		MaxAge        uint32
	}{p.ID, p.Policy.Digest(), p.MinimumProofs, p.MinimumLevel, p.MaxProofAgeSeconds})
	return evidence.Digest(raw)
}

// AutonomyProof is loaded only from authoritative verifier storage. The actor and
// challenge must be bound into that verifier's retained input provenance; neither
// uploaded Passports nor agent claims may supply these bindings.
type AutonomyProof struct {
	Actor           authorization.Principal
	ChallengeDigest string
	Passport        passport.Passport
	ValidUntil      time.Time
}
type EarnedBinding struct {
	PolicyID        string `json:"policy_id"`
	ChallengeDigest string `json:"challenge_digest"`
	ProofSetDigest  string `json:"proof_set_digest"`
}

func (b EarnedBinding) valid() bool {
	return evidence.ValidIdentifier(b.PolicyID) && validHash(b.ChallengeDigest) && validHash(b.ProofSetDigest)
}

// AutonomyCompiler has no edition, entitlement, transport or storage dependencies.
// Commercial limits are supplied as bounded numeric policy, never plan labels.
type AutonomyCompiler struct{}

func (AutonomyCompiler) Challenge(actor authorization.Principal, contract intent.IntentContract, inv Inventory, p AutonomyPolicy, calls []Call, maxCalls uint32) (Capabilities, string, error) {
	if actor.Validate() != nil || p.Digest() == "" || maxCalls == 0 || maxCalls > MaxCalls {
		return Capabilities{}, "", ErrInvalid
	}
	effective := p.Policy
	if effective.CallBudget > maxCalls {
		effective.CallBudget = maxCalls
	}
	if len(calls) > int(effective.CallBudget) {
		return Capabilities{}, "", ErrDenied
	}
	caps, err := Compile(contract, inv, effective, calls)
	if err != nil {
		return Capabilities{}, "", err
	}
	raw, _ := json.Marshal(struct {
		Actor                            authorization.Principal
		PolicyDigest, CapabilitiesDigest string
	}{actor, p.Digest(), caps.Digest()})
	return caps, evidence.Digest(append([]byte("safepatch.earned-challenge.v1\n"), raw...)), nil
}

type EarnedEvaluation struct {
	Capabilities Capabilities
	ValidUntil   time.Time
}

func (k AutonomyCompiler) Evaluate(ctx context.Context, actor authorization.Principal, contract intent.IntentContract, inv Inventory, p AutonomyPolicy, calls []Call, maxCalls uint32, records []AutonomyProof, now time.Time) (EarnedEvaluation, error) {
	bad := EarnedEvaluation{}
	if ctx == nil || !validTime(now) || len(records) < int(p.MinimumProofs) || len(records) > 32 {
		return bad, ErrDenied
	}
	if err := ctx.Err(); err != nil {
		return bad, err
	}
	caps, challenge, err := k.Challenge(actor, contract, inv, p, calls, maxCalls)
	if err != nil {
		return bad, err
	}
	fields, _ := caps.Fields()
	fingerprints := []string{}
	seenProofs := map[string]bool{}
	seenRuns := map[string]bool{}
	expiry := now.Add(24 * time.Hour)
	for _, r := range records {
		if err := ctx.Err(); err != nil {
			return bad, err
		}
		f, err := r.Passport.Fields()
		digest := r.Passport.Digest()
		if err != nil || r.Actor != actor || r.ChallengeDigest != challenge || f.Change.OrganizationID != p.Policy.Scope.OrganizationID || f.Change.RepositoryID != p.Policy.Scope.RepositoryID || f.IntentDigest != fields.IntentDigest || f.VerificationDecision != trust.Pass || f.PolicyDecision.Status != evidence.StatusPass || f.ProofLevel.Rank() < p.MinimumLevel.Rank() || seenProofs[digest] || seenRuns[f.RunID] || !validTime(r.ValidUntil) || f.IssuedAt.After(now) || !r.ValidUntil.After(f.IssuedAt) || !now.Before(r.ValidUntil) {
			return bad, ErrDenied
		}
		ageExpiry := f.IssuedAt.Add(time.Duration(p.MaxProofAgeSeconds) * time.Second)
		if !now.Before(ageExpiry) {
			return bad, ErrDenied
		}
		if ageExpiry.Before(expiry) {
			expiry = ageExpiry
		}
		if r.ValidUntil.Before(expiry) {
			expiry = r.ValidUntil
		}
		seenProofs[digest] = true
		seenRuns[f.RunID] = true
		raw, _ := json.Marshal(struct {
			Digest     string
			ValidUntil time.Time
		}{digest, r.ValidUntil.UTC()})
		fingerprints = append(fingerprints, evidence.Digest(raw))
	}
	slices.Sort(fingerprints)
	fields.Earned = &EarnedBinding{PolicyID: p.ID, ChallengeDigest: challenge, ProofSetDigest: evidence.Digest([]byte(strings.Join(fingerprints, "\n")))}
	caps, err = sealCapabilities(fields)
	if err != nil {
		return bad, err
	}
	return EarnedEvaluation{caps, expiry}, nil
}
