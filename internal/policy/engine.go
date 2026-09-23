package policy

import (
	"context"
	"slices"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/tenant"
)

// Input is assembled by trusted orchestration from an authorized repository/run.
// Risk is a known integer 0..100; proof level is a configured ordinal. Nil means
// unknown, never zero. This engine does not calculate risk or mint proof levels.
type Input struct {
	QuorumComplete bool
	EvidenceEdges  []graph.Edge
	TenantID       string
	RepositoryID   string
	ChangeID       string
	RunID          string
	AsOf           time.Time
	Criticality    Criticality
	RiskScore      *int
	ProofLevel     *uint32
	Evidence       []evidence.Evidence
}

type ReasonCode string

const (
	Satisfied           ReasonCode = "SATISFIED"
	NotApplicable       ReasonCode = "NOT_APPLICABLE"
	MissingCriticality  ReasonCode = "MISSING_CRITICALITY"
	MissingRisk         ReasonCode = "MISSING_RISK"
	RiskExceeded        ReasonCode = "RISK_EXCEEDED"
	MissingProof        ReasonCode = "MISSING_PROOF"
	ProofInsufficient   ReasonCode = "PROOF_INSUFFICIENT"
	MissingEvidence     ReasonCode = "MISSING_EVIDENCE"
	EvidenceFailed      ReasonCode = "EVIDENCE_FAILED"
	EvidenceIncomplete  ReasonCode = "EVIDENCE_INCOMPLETE"
	EvidenceUnqualified ReasonCode = "EVIDENCE_UNQUALIFIED"
	NoApplicableRules   ReasonCode = "NO_APPLICABLE_RULES"
	InvalidInput        ReasonCode = "INVALID_INPUT"
	InvalidPolicy       ReasonCode = "INVALID_POLICY"
	Canceled            ReasonCode = "CANCELED"
)

type CheckResult struct {
	Quorum      *quorum.ResultFields `json:"quorum,omitempty"`
	Code        ReasonCode           `json:"code"`
	Status      evidence.Status      `json:"status"`
	Producer    string               `json:"producer,omitempty"`
	Type        string               `json:"type,omitempty"`
	EvidenceIDs []string             `json:"evidence_ids,omitempty"`
}
type RuleResult struct {
	RuleID string          `json:"rule_id"`
	Status evidence.Status `json:"status"`
	Checks []CheckResult   `json:"checks"`
}

// Result is diagnostic evaluation, never an execution or production approval.
type Result struct {
	Schema        string          `json:"schema"`
	PolicyID      string          `json:"policy_id"`
	PolicyVersion string          `json:"policy_version"`
	PolicyHash    string          `json:"policy_hash"`
	TenantID      string          `json:"tenant_id"`
	RepositoryID  string          `json:"repository_id"`
	ChangeID      string          `json:"change_id"`
	RunID         string          `json:"run_id"`
	Status        evidence.Status `json:"status"`
	Code          ReasonCode      `json:"code"`
	Rules         []RuleResult    `json:"rules"`
}
type Engine struct{}

func (Engine) Evaluate(ctx context.Context, p Policy, in Input) (Result, error) {
	failure := func(code ReasonCode, err error) (Result, error) {
		return Result{Schema: "safepatch.policy-result.v1", Status: evidence.StatusError, Code: code, Rules: []RuleResult{}}, err
	}
	if ctx == nil {
		return failure(InvalidInput, ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return failure(Canceled, err)
	}
	d, err := p.Document()
	if err != nil {
		return failure(InvalidPolicy, err)
	}
	if d.TenantID != in.TenantID || !tenant.ValidID(in.TenantID) || !tenant.ValidID(in.RepositoryID) || !evidence.ValidIdentifier(in.ChangeID) || !evidence.ValidIdentifier(in.RunID) || in.AsOf.IsZero() || in.AsOf.UTC().Year() < 1 || in.AsOf.UTC().Year() > 9999 || !in.Criticality.valid() || in.RiskScore != nil && (*in.RiskScore < 0 || *in.RiskScore > 100) || len(in.Evidence) > MaxEvidence {
		return failure(InvalidInput, ErrInvalidInput)
	}
	// Decode once, cap aggregate bytes, and bind every observation to the run.
	// Reject foreign or duplicate observations even if no rule would inspect them.
	index := map[[2]string][]evidence.EvidenceFields{}
	seen := map[string]bool{}
	total := 0
	for _, node := range in.Evidence {
		if err := ctx.Err(); err != nil {
			return failure(Canceled, err)
		}
		raw, err := node.CanonicalJSON()
		total += len(raw)
		if err != nil || total > MaxInputBytes {
			return failure(InvalidInput, ErrInvalidInput)
		}
		f, err := node.Fields()
		if err != nil || f.TenantID != in.TenantID || f.ChangeID != in.ChangeID || f.RunID != in.RunID || f.CreatedAt.After(in.AsOf) || seen[f.EvidenceID] {
			return failure(InvalidInput, ErrInvalidInput)
		}
		seen[f.EvidenceID] = true
		key := [2]string{f.Producer, f.Type}
		index[key] = append(index[key], f)
	}
	result := Result{Schema: "safepatch.policy-result.v1", PolicyID: d.PolicyID, PolicyVersion: d.Version, PolicyHash: p.Hash(), TenantID: in.TenantID, RepositoryID: in.RepositoryID, ChangeID: in.ChangeID, RunID: in.RunID, Status: evidence.StatusPass, Code: Satisfied, Rules: []RuleResult{}}
	applied := 0
	for _, rule := range d.Rules {
		if err := ctx.Err(); err != nil {
			return failure(Canceled, err)
		}
		r := RuleResult{RuleID: rule.ID, Status: evidence.StatusPass, Checks: []CheckResult{}}
		add := func(code ReasonCode, status evidence.Status) {
			r.Checks = append(r.Checks, CheckResult{Code: code, Status: status})
			r.Status = combine(r.Status, status)
		}
		if len(rule.When.RepositoryIDs) > 0 && !slices.Contains(rule.When.RepositoryIDs, in.RepositoryID) {
			r.Status = evidence.StatusSkipped
			r.Checks = append(r.Checks, CheckResult{Code: NotApplicable, Status: r.Status})
		} else if len(rule.When.Criticalities) > 0 && in.Criticality == CriticalityUnknown {
			add(MissingCriticality, evidence.StatusUnknown)
			applied++
		} else if len(rule.When.Criticalities) > 0 && !slices.Contains(rule.When.Criticalities, in.Criticality) {
			r.Status = evidence.StatusSkipped
			r.Checks = append(r.Checks, CheckResult{Code: NotApplicable, Status: r.Status})
		} else {
			applied++
			if threshold := rule.Require.MaxRiskScore; threshold != nil {
				switch {
				case in.RiskScore == nil:
					add(MissingRisk, evidence.StatusUnknown)
				case *in.RiskScore > *threshold:
					add(RiskExceeded, evidence.StatusFail)
				default:
					add(Satisfied, evidence.StatusPass)
				}
			}
			if level := rule.Require.MinProofLevel; level != nil {
				switch {
				case in.ProofLevel == nil:
					add(MissingProof, evidence.StatusUnknown)
				case *in.ProofLevel < *level:
					add(ProofInsufficient, evidence.StatusFail)
				default:
					add(Satisfied, evidence.StatusPass)
				}
			}
			for _, req := range rule.Require.Evidence {
				c := checkEvidence(req, index[[2]string{req.Producer, req.Type}])
				r.Checks = append(r.Checks, c)
				r.Status = combine(r.Status, c.Status)
			}
			if rule.Require.Quorum != nil {
				configured, err := quorum.New(*rule.Require.Quorum)
				if err != nil {
					return failure(InvalidPolicy, ErrInvalidPolicy)
				}
				assessed, err := (quorum.EvidenceQuorum{}).Evaluate(ctx, configured, quorum.Input{TenantID: in.TenantID, RepositoryID: in.RepositoryID, ChangeID: in.ChangeID, RunID: in.RunID, AsOf: in.AsOf, Complete: in.QuorumComplete, Evidence: in.Evidence, Edges: in.EvidenceEdges})
				if err != nil {
					return failure(InvalidInput, err)
				}
				fields, _ := assessed.Fields()
				code := Satisfied
				if fields.Status == evidence.StatusFail {
					code = EvidenceFailed
				} else if fields.Status != evidence.StatusPass {
					code = MissingEvidence
				}
				r.Checks = append(r.Checks, CheckResult{Code: code, Status: fields.Status, Quorum: &fields})
				r.Status = combine(r.Status, fields.Status)
			}
		}
		result.Rules = append(result.Rules, r)
		if r.Status != evidence.StatusSkipped {
			result.Status = combine(result.Status, r.Status)
		}
	}
	if applied == 0 {
		result.Status = evidence.StatusUnknown
		result.Code = NoApplicableRules
	} else if result.Status != evidence.StatusPass {
		// The first check with the aggregate status explains the result in stable
		// declaration order. FAIL dominates UNKNOWN; neither becomes PASS.
	findReason:
		for _, r := range result.Rules {
			for _, c := range r.Checks {
				if c.Status == result.Status {
					result.Code = c.Code
					break findReason
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return failure(Canceled, err)
	}
	return result, nil
}

func combine(a, b evidence.Status) evidence.Status {
	if a == evidence.StatusFail || b == evidence.StatusFail {
		return evidence.StatusFail
	}
	if a == evidence.StatusUnknown || b == evidence.StatusUnknown {
		return evidence.StatusUnknown
	}
	return evidence.StatusPass
}
func checkEvidence(req EvidenceRequirement, observations []evidence.EvidenceFields) CheckResult {
	c := CheckResult{Code: Satisfied, Status: evidence.StatusPass, Producer: req.Producer, Type: req.Type, EvidenceIDs: []string{}}
	if len(observations) == 0 {
		c.Code = MissingEvidence
		c.Status = evidence.StatusUnknown
		return c
	}
	for _, f := range observations {
		c.EvidenceIDs = append(c.EvidenceIDs, f.EvidenceID)
		status, code := evidence.StatusPass, Satisfied
		switch f.Status {
		case evidence.StatusFail, evidence.StatusWarn:
			status, code = evidence.StatusFail, EvidenceFailed
		case evidence.StatusPass:
			if f.Confidence < req.MinConfidence || req.Deterministic && !f.Deterministic {
				status, code = evidence.StatusUnknown, EvidenceUnqualified
			}
		default:
			status, code = evidence.StatusUnknown, EvidenceIncomplete
		}
		if status == evidence.StatusFail || status == evidence.StatusUnknown && c.Status != evidence.StatusFail {
			// Fixed reason precedence also makes reordered observations equivalent.
			if status != c.Status || code < c.Code {
				c.Code = code
			}
			c.Status = combine(c.Status, status)
		}
	}
	slices.Sort(c.EvidenceIDs)
	return c
}
