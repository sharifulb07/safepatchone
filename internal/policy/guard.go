package policy

import (
	"context"
	"safepatch/internal/proof/quorum"
	"slices"
)

type GuardFinding struct {
	RuleID string `json:"rule_id"`
	Code   string `json:"code"`
}

// PolicyGuard conservatively preserves each baseline obligation. Renaming or
// restructuring a rule is not assumed equivalent. It never loads candidate code.
type PolicyGuard struct{}

func (PolicyGuard) Check(ctx context.Context, base, head Policy) ([]GuardFinding, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, e1 := base.Document()
	b, e2 := head.Document()
	if e1 != nil || e2 != nil || a.TenantID != b.TenantID {
		return nil, ErrInvalidPolicy
	}
	out := []GuardFinding{}
	add := func(id, code string) { out = append(out, GuardFinding{id, code}) }
	index := map[string]Rule{}
	for _, r := range b.Rules {
		index[r.ID] = r
	}
	if a.PolicyID != b.PolicyID {
		add("", "POLICY_ID_CHANGED")
	}
	for _, old := range a.Rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, ok := index[old.ID]
		if !ok {
			add(old.ID, "RULE_REMOVED")
			continue
		}
		if !selectorContains(next.When.RepositoryIDs, old.When.RepositoryIDs) || !selectorContains(next.When.Criticalities, old.When.Criticalities) {
			add(old.ID, "APPLICABILITY_NARROWED")
		}
		x, y := old.Require, next.Require
		if x.Quorum != nil {
			previous, _ := quorum.New(*x.Quorum)
			if y.Quorum == nil {
				add(old.ID, "QUORUM_REQUIREMENT_CHANGED")
			} else {
				current, _ := quorum.New(*y.Quorum)
				if current.Digest() != previous.Digest() {
					add(old.ID, "QUORUM_REQUIREMENT_CHANGED")
				}
			}
		}
		if x.MaxRiskScore != nil && (y.MaxRiskScore == nil || *y.MaxRiskScore > *x.MaxRiskScore) {
			add(old.ID, "RISK_REQUIREMENT_REDUCED")
		}
		if x.MinProofLevel != nil && (y.MinProofLevel == nil || *y.MinProofLevel < *x.MinProofLevel) {
			add(old.ID, "PROOF_REQUIREMENT_REDUCED")
		}
		requirements := map[[2]string]EvidenceRequirement{}
		for _, e := range y.Evidence {
			requirements[[2]string{e.Producer, e.Type}] = e
		}
		for _, e := range x.Evidence {
			n, ok := requirements[[2]string{e.Producer, e.Type}]
			if !ok || n.MinConfidence < e.MinConfidence || e.Deterministic && !n.Deterministic {
				add(old.ID, "EVIDENCE_REQUIREMENT_REDUCED")
			}
		}
	}
	return out, nil
}
func selectorContains[T comparable](next, old []T) bool {
	if len(next) == 0 {
		return true
	}
	if len(old) == 0 {
		return false
	}
	for _, v := range old {
		if !slices.Contains(next, v) {
			return false
		}
	}
	return true
}
