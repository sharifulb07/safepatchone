package trust

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"safepatch/internal/domain"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/proof"
)

// AuthorizationDecision is a technical verdict bound to evaluated inputs. It
// neither grants actor permissions nor executes a production operation.
type AuthorizationDecision string

const (
	Authorize AuthorizationDecision = "AUTHORIZE"
	Review    AuthorizationDecision = "REVIEW"
	Escrow    AuthorizationDecision = "ESCROW"
	Block     AuthorizationDecision = "BLOCK"
	Reverify  AuthorizationDecision = "REVERIFY"
)
const KernelSchema = "safepatch.trust-decision.v1"

var ErrKernelInput = errors.New("invalid TrustKernel input")

// Controls is trusted security policy, not a commercial entitlement bundle.
// Risk scores range from 0 to 100; impact thresholds use ordered criticalities.
type Controls struct {
	MinimumProofLevel  proof.Level        `json:"minimum_proof_level"`
	MaxProofAgeSeconds uint32             `json:"max_proof_age_seconds"`
	ReviewRiskAbove    int                `json:"review_risk_above"`
	EscrowImpactAt     policy.Criticality `json:"escrow_impact_at"`
}

func DefaultControls(level proof.Level) Controls {
	return Controls{level, 600, 50, policy.CriticalityHigh}
}
func (c Controls) valid() bool {
	return c.MinimumProofLevel.Rank() >= 0 && c.MaxProofAgeSeconds > 0 && c.MaxProofAgeSeconds <= 86400 && c.ReviewRiskAbove >= 0 && c.ReviewRiskAbove <= 100 && impactRank(c.EscrowImpactAt) > 0
}
func (c Controls) Validate() error {
	if !c.valid() {
		return ErrKernelInput
	}
	return nil
}

// Coverage maps independently established proof obligations to retained evidence
// IDs. A caller cannot declare completeness with a percentage or an empty set.
type Coverage struct {
	Complete bool                `json:"complete"`
	Required []string            `json:"required"`
	Observed map[string][]string `json:"observed"`
}
type Freshness struct {
	VerifiedAt time.Time `json:"verified_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type Origin string

const (
	OriginVerifier Origin = "VERIFIER"
	OriginLLM      Origin = "LLM"
	OriginUnknown  Origin = "UNKNOWN"
)

// Provenance is an attestation verification result from trusted orchestration.
// Canonical JSON, hashes, signatures without trusted keys, and uploaded claims
// cannot establish StatusPass. The kernel performs no I/O or key discovery.
type Provenance struct {
	Status         evidence.Status `json:"status"`
	Origin         Origin          `json:"origin"`
	ChangeID       string          `json:"change_id"`
	RunID          string          `json:"run_id"`
	IntentDigest   string          `json:"intent_digest"`
	EvidenceDigest string          `json:"evidence_digest"`
	PolicyDigest   string          `json:"policy_digest"`
}
type Assessment struct {
	Risk           *int               `json:"risk"`
	BusinessImpact policy.Criticality `json:"business_impact"`
	ProofFreshness Freshness          `json:"proof_freshness"`
	Provenance     Provenance         `json:"provenance"`
}

func UnknownAssessment() Assessment {
	return Assessment{BusinessImpact: policy.CriticalityUnknown, Provenance: Provenance{Status: evidence.StatusUnknown, Origin: OriginUnknown}}
}

type KernelInput struct {
	// QuorumComplete attests complete collection of relevant observations, not
	// satisfaction of their proof obligations (ProofCoverage.Complete).
	QuorumComplete bool                  `json:"quorum_complete,omitempty"`
	EvidenceEdges  []graph.Edge          `json:"evidence_edges,omitempty"`
	Change         domain.Change         `json:"change"`
	RunID          string                `json:"run_id"`
	AsOf           time.Time             `json:"as_of"`
	Intent         intent.IntentContract `json:"intent"`
	Evidence       []evidence.Evidence   `json:"evidence"`
	ProofLevel     proof.Level           `json:"proof_level"`
	Policy         policy.Policy         `json:"policy"`
	Controls       Controls              `json:"controls"`
	ProofCoverage  Coverage              `json:"proof_coverage"`
	Assessment
}
type KernelResult struct {
	Schema      string                `json:"schema"`
	Decision    AuthorizationDecision `json:"decision"`
	Reasons     []string              `json:"reasons"`
	InputDigest string                `json:"input_digest"`
	Policy      policy.Result         `json:"policy"`
}

// TrustKernel owns the final deterministic technical decision. Commercial
// availability and actor admission are resolved before verification, outside it.
// Precedence: BLOCK > REVERIFY > ESCROW > REVIEW > AUTHORIZE.
type TrustKernel struct{}

func (TrustKernel) Evaluate(ctx context.Context, in KernelInput) (KernelResult, error) {
	r := KernelResult{Schema: KernelSchema, Decision: Block, Reasons: []string{"INVALID_INPUT"}}
	if ctx == nil {
		return r, ErrKernelInput
	}
	if err := ctx.Err(); err != nil {
		r.Reasons = []string{"CANCELED"}
		return r, err
	}
	if in.Change.Validate() != nil || !evidence.ValidIdentifier(in.RunID) || !kernelTime(in.AsOf) || !in.Controls.valid() || in.ProofLevel != "UNKNOWN" && in.ProofLevel.Rank() < 0 || impactRank(in.BusinessImpact) < 0 || in.Risk != nil && (*in.Risk < 0 || *in.Risk > 100) {
		return r, ErrKernelInput
	}
	intentHash, err := in.Intent.ContentHash()
	// Bound even unverified metadata before hashing or serializing it.
	if len(in.Provenance.ChangeID) > 128 || len(in.Provenance.RunID) > 128 || len(in.Provenance.IntentDigest) > 64 || len(in.Provenance.EvidenceDigest) > 64 || len(in.Provenance.PolicyDigest) > 64 {
		return r, ErrKernelInput
	}
	if err != nil {
		return r, ErrKernelInput
	}
	evidenceHash, err := EvidenceDigest(in.Evidence)
	if err != nil {
		return r, err
	}
	var level *uint32
	if in.ProofLevel.Rank() >= 0 {
		n := uint32(in.ProofLevel.Rank())
		level = &n
	}
	if len(in.EvidenceEdges) > 512 {
		return r, ErrKernelInput
	}
	nodeIDs := map[string]bool{}
	for _, node := range in.Evidence {
		f, _ := node.Fields()
		nodeIDs[f.EvidenceID] = true
	}
	edgeIDs := map[string]bool{}
	for _, edge := range in.EvidenceEdges {
		if edge.Validate() != nil || edge.TenantID != in.Change.OrganizationID || edge.CreatedAt.After(in.AsOf) || edgeIDs[edge.EdgeID] || !nodeIDs[edge.FromEvidenceID] || !nodeIDs[edge.ToEvidenceID] {
			return r, ErrKernelInput
		}
		edgeIDs[edge.EdgeID] = true
	}
	r.Policy, err = (policy.Engine{}).Evaluate(ctx, in.Policy, policy.Input{TenantID: in.Change.OrganizationID, RepositoryID: in.Change.RepositoryID, ChangeID: in.Change.ID(), RunID: in.RunID, AsOf: in.AsOf, Criticality: in.BusinessImpact, RiskScore: in.Risk, ProofLevel: level, Evidence: in.Evidence, QuorumComplete: in.QuorumComplete, EvidenceEdges: in.EvidenceEdges})
	if err != nil {
		return r, err
	}
	blocking, missing, review := []string{}, []string{}, []string{}
	add := func(list *[]string, reason string) {
		if !slices.Contains(*list, reason) {
			*list = append(*list, reason)
		}
	}
	observed := map[string]bool{}
	fresh := in.ProofFreshness
	if fresh.VerifiedAt.IsZero() || fresh.ExpiresAt.IsZero() {
		add(&missing, "FRESHNESS_UNKNOWN")
	} else if !kernelTime(fresh.VerifiedAt) || !kernelTime(fresh.ExpiresAt) || !fresh.ExpiresAt.After(fresh.VerifiedAt) || fresh.VerifiedAt.After(in.AsOf) {
		add(&blocking, "FRESHNESS_INVALID")
	} else if !in.AsOf.Before(fresh.ExpiresAt) || !in.AsOf.Before(fresh.VerifiedAt.Add(time.Duration(in.Controls.MaxProofAgeSeconds)*time.Second)) {
		add(&missing, "PROOF_STALE")
	}
	for _, node := range in.Evidence {
		if err := ctx.Err(); err != nil {
			r.Reasons = []string{"CANCELED"}
			return r, err
		}
		f, _ := node.Fields()
		observed[f.EvidenceID] = true
		if f.InputHashes["intent"] != intentHash {
			add(&blocking, "INTENT_BINDING_MISMATCH")
		}
		if f.Status == evidence.StatusFail {
			add(&blocking, "EVIDENCE_FAILED")
		} else if f.Status != evidence.StatusPass {
			add(&missing, "EVIDENCE_INCOMPLETE")
		}
		if !in.AsOf.Before(f.CreatedAt.Add(time.Duration(in.Controls.MaxProofAgeSeconds) * time.Second)) {
			add(&missing, "PROOF_STALE")
		}
		if !fresh.VerifiedAt.IsZero() && f.CreatedAt.After(fresh.VerifiedAt) {
			add(&blocking, "FRESHNESS_INVALID")
		}
	}
	c := in.ProofCoverage
	if len(c.Required) > proof.MaxObligations || len(c.Observed) > proof.MaxObligations {
		return r, ErrKernelInput
	}
	if !c.Complete || len(c.Required) == 0 || len(in.Evidence) == 0 {
		add(&missing, "COVERAGE_INCOMPLETE")
	}
	required := map[string]bool{}
	count := 0
	for _, id := range c.Required {
		if !evidence.ValidIdentifier(id) || required[id] {
			return r, ErrKernelInput
		}
		required[id] = true
		if len(c.Observed[id]) == 0 {
			add(&missing, "COVERAGE_INCOMPLETE")
		}
	}
	for id, refs := range c.Observed {
		if !evidence.ValidIdentifier(id) || !required[id] || len(refs) > policy.MaxEvidence {
			return r, ErrKernelInput
		}
		seen := map[string]bool{}
		for _, ref := range refs {
			count++
			if count > 1024 || !evidence.ValidIdentifier(ref) || !observed[ref] || seen[ref] {
				return r, ErrKernelInput
			}
			seen[ref] = true
		}
	}
	if level == nil || in.ProofLevel.Rank() < in.Controls.MinimumProofLevel.Rank() {
		add(&missing, "PROOF_LEVEL_INSUFFICIENT")
	}
	switch r.Policy.Status {
	case evidence.StatusFail:
		add(&blocking, "POLICY_FAILED")
	case evidence.StatusPass:
	default:
		add(&missing, "POLICY_INCOMPLETE")
	}
	p := in.Provenance
	if !p.Status.Valid() || p.Origin != OriginVerifier && p.Origin != OriginLLM && p.Origin != OriginUnknown {
		return r, ErrKernelInput
	}
	if p.Origin == OriginLLM || p.Status == evidence.StatusFail {
		add(&blocking, "PROVENANCE_REJECTED")
	} else if p.Status != evidence.StatusPass || p.Origin != OriginVerifier {
		add(&missing, "PROVENANCE_UNVERIFIED")
	} else if p.ChangeID != in.Change.ID() || p.RunID != in.RunID || p.IntentDigest != intentHash || p.EvidenceDigest != evidenceHash || p.PolicyDigest != in.Policy.Hash() {
		add(&blocking, "PROVENANCE_BINDING_MISMATCH")
	}
	if in.Risk == nil {
		add(&review, "RISK_UNKNOWN")
	} else if *in.Risk > in.Controls.ReviewRiskAbove {
		add(&review, "RISK_REVIEW_REQUIRED")
	}
	if in.BusinessImpact == policy.CriticalityUnknown {
		add(&review, "IMPACT_UNKNOWN")
	}
	r.InputDigest = kernelDigest(in, intentHash, evidenceHash)
	if r.InputDigest == "" {
		return r, ErrKernelInput
	}
	if err := ctx.Err(); err != nil {
		r.Reasons = []string{"CANCELED"}
		return r, err
	}
	switch {
	case len(blocking) > 0:
		r.Decision = Block
		r.Reasons = blocking
	case len(missing) > 0:
		r.Decision = Reverify
		r.Reasons = missing
	case impactRank(in.BusinessImpact) >= impactRank(in.Controls.EscrowImpactAt):
		r.Decision = Escrow
		r.Reasons = []string{"IMPACT_ESCROW_REQUIRED"}
	case len(review) > 0:
		r.Decision = Review
		r.Reasons = review
	default:
		r.Decision = Authorize
		r.Reasons = []string{"TECHNICAL_REQUIREMENTS_SATISFIED"}
	}
	slices.Sort(r.Reasons)
	return r, nil
}

// EvidenceDigest pins a set of immutable observations without exposing payloads.
func EvidenceDigest(nodes []evidence.Evidence) (string, error) {
	if len(nodes) > policy.MaxEvidence {
		return "", ErrKernelInput
	}
	hashes := []string{}
	ids := map[string]bool{}
	total := 0
	for _, node := range nodes {
		raw, err := node.CanonicalJSON()
		total += len(raw)
		if err != nil || total > policy.MaxInputBytes {
			return "", ErrKernelInput
		}
		f, err := node.Fields()
		if err != nil || ids[f.EvidenceID] {
			return "", ErrKernelInput
		}
		ids[f.EvidenceID] = true
		hashes = append(hashes, f.ContentHash)
	}
	slices.Sort(hashes)
	raw, _ := json.Marshal(hashes)
	return evidence.Digest(raw), nil
}
func impactRank(c policy.Criticality) int {
	return slices.Index([]policy.Criticality{policy.CriticalityUnknown, policy.CriticalityLow, policy.CriticalityMedium, policy.CriticalityHigh, policy.CriticalityCritical}, c)
}
func kernelTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func kernelDigest(in KernelInput, intentHash, evidenceHash string) string {
	edges := slices.Clone(in.EvidenceEdges)
	for i := range edges {
		edges[i].CreatedAt = edges[i].CreatedAt.UTC()
	}
	slices.SortFunc(edges, func(a, b graph.Edge) int { return compareEdgeID(a.EdgeID, b.EdgeID) })
	// Sets are normalized on copies; evaluating never mutates caller input.
	coverage := Coverage{Complete: in.ProofCoverage.Complete, Required: slices.Clone(in.ProofCoverage.Required), Observed: map[string][]string{}}
	slices.Sort(coverage.Required)
	for id, refs := range in.ProofCoverage.Observed {
		coverage.Observed[id] = slices.Clone(refs)
		slices.Sort(coverage.Observed[id])
	}
	assessment := in.Assessment
	assessment.ProofFreshness.VerifiedAt = assessment.ProofFreshness.VerifiedAt.UTC()
	assessment.ProofFreshness.ExpiresAt = assessment.ProofFreshness.ExpiresAt.UTC()
	raw, err := json.Marshal(struct {
		Schema                   string
		Change                   domain.Change
		RunID                    string
		AsOf                     time.Time
		Intent, Evidence, Policy string
		Level                    proof.Level
		Controls                 Controls
		Coverage                 Coverage
		Assessment               Assessment
		Edges                    []graph.Edge `json:",omitempty"`
		QuorumComplete           bool         `json:",omitempty"`
	}{KernelSchema, in.Change, in.RunID, in.AsOf.UTC(), intentHash, evidenceHash, in.Policy.Hash(), in.ProofLevel, in.Controls, coverage, assessment, edges, in.QuorumComplete})
	if err != nil {
		return ""
	}
	return evidence.Digest(raw)
}

func compareEdgeID(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
