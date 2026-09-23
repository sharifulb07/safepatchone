package runtime

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/replay"
)

type InvariantTerm struct {
	Metric      string `json:"metric"`
	Coefficient string `json:"coefficient"`
}

// BusinessInvariant evaluates sum(coefficient * metric) EQ/LE/GE bound for
// EVERY observed case. No script, network, regex or LLM executes a rule.
type BusinessInvariant struct {
	ID       string          `json:"id"`
	Terms    []InvariantTerm `json:"terms"`
	Operator string          `json:"operator"`
	Bound    string          `json:"bound"`
}
type InvariantFields struct {
	Schema  string              `json:"schema"`
	Scope   replay.Scope        `json:"scope"`
	ID      string              `json:"policy_id"`
	Version string              `json:"version"`
	Rules   []BusinessInvariant `json:"rules"`
}
type InvariantPolicy struct{ canonical string }

func (p InvariantPolicy) Digest() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(p.canonical))
}
func (p InvariantPolicy) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalidBehavior
	}
	return []byte(p.canonical), nil
}
func (p InvariantPolicy) Fields() (InvariantFields, error) {
	var f InvariantFields
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &f) != nil {
		return f, ErrInvalidBehavior
	}
	return f, nil
}
func NewInvariantPolicy(f InvariantFields) (InvariantPolicy, error) {
	if f.Schema != "safepatch.business-invariants.v1" || f.Scope.Validate() != nil || !evidence.ValidIdentifier(f.ID) || !evidence.ValidIdentifier(f.Version) || len(f.Rules) == 0 || len(f.Rules) > MaxRules {
		return InvariantPolicy{}, ErrInvalidBehavior
	}
	ids := map[string]bool{}
	for _, r := range f.Rules {
		if !evidence.ValidIdentifier(r.ID) || ids[r.ID] || len(r.Terms) == 0 || len(r.Terms) > MaxMetrics || (r.Operator != "EQ" && r.Operator != "LE" && r.Operator != "GE") {
			return InvariantPolicy{}, ErrInvalidBehavior
		}
		ids[r.ID] = true
		if _, ok := number(r.Bound); !ok {
			return InvariantPolicy{}, ErrInvalidBehavior
		}
		names := map[string]bool{}
		for _, term := range r.Terms {
			n, ok := number(term.Coefficient)
			if !ok || n.Sign() == 0 || !evidence.ValidIdentifier(term.Metric) || names[term.Metric] {
				return InvariantPolicy{}, ErrInvalidBehavior
			}
			names[term.Metric] = true
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxArtifactBytes {
		return InvariantPolicy{}, ErrInvalidBehavior
	}
	return InvariantPolicy{string(raw)}, nil
}
func DecodeInvariantPolicy(raw []byte) (InvariantPolicy, error) {
	var f InvariantFields
	if strictObject(raw, &f) != nil {
		return InvariantPolicy{}, ErrInvalidBehavior
	}
	return NewInvariantPolicy(f)
}

type InvariantDecision string

const InvariantSatisfied InvariantDecision = "SATISFIED"
const InvariantViolated InvariantDecision = "VIOLATED"
const InvariantUnknown InvariantDecision = "UNKNOWN"

type InvariantCheck struct {
	RuleID   string            `json:"rule_id"`
	CaseID   string            `json:"case_id"`
	Decision InvariantDecision `json:"decision"`
}
type InvariantResult struct {
	Schema         string            `json:"schema"`
	Scope          replay.Scope      `json:"scope"`
	BehaviorDigest string            `json:"behavior_digest"`
	PolicyDigest   string            `json:"policy_digest"`
	Decision       InvariantDecision `json:"decision"`
	Checks         []InvariantCheck  `json:"checks"`
}

// BusinessInvariantEngine is shared by all deployments. Results are diagnostic;
// SATISFIED means only that the supplied observations satisfy the supplied rules.
type BusinessInvariantEngine struct{}

func (BusinessInvariantEngine) Evaluate(ctx context.Context, d BehaviorDNA, p InvariantPolicy, asOf time.Time) (InvariantResult, error) {
	result := InvariantResult{Schema: "safepatch.invariant-result.v1", Decision: InvariantUnknown, Checks: []InvariantCheck{}}
	if ctx == nil {
		return result, ErrInvalidBehavior
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	f, e1 := d.Fields()
	rules, e2 := p.Fields()
	if e1 != nil || e2 != nil || f.Scope != rules.Scope || asOf.IsZero() || asOf.Before(f.CreatedAt) || !asOf.Before(f.ExpiresAt) {
		return result, ErrInvalidBehavior
	}
	result.Scope = f.Scope
	result.BehaviorDigest = d.Digest()
	result.PolicyDigest = p.Digest()
	result.Decision = InvariantSatisfied
	for _, r := range rules.Rules {
		for _, c := range f.Cases {
			if err := ctx.Err(); err != nil {
				result.Decision = InvariantUnknown
				return result, err
			}
			decision := InvariantUnknown
			if c.Complete {
				total := new(big.Rat)
				known := true
				for _, term := range r.Terms {
					value, ok := c.Values[term.Metric]
					if !ok {
						known = false
						break
					}
					v, _ := new(big.Rat).SetString(value)
					coefficient, _ := number(term.Coefficient)
					total.Add(total, new(big.Rat).Mul(v, coefficient))
				}
				if known {
					bound, _ := number(r.Bound)
					cmp := total.Cmp(bound)
					satisfied := r.Operator == "EQ" && cmp == 0 || r.Operator == "LE" && cmp <= 0 || r.Operator == "GE" && cmp >= 0
					decision = InvariantViolated
					if satisfied {
						decision = InvariantSatisfied
					}
				}
			}
			result.Checks = append(result.Checks, InvariantCheck{RuleID: r.ID, CaseID: c.ID, Decision: decision})
			if decision == InvariantUnknown {
				result.Decision = InvariantUnknown
			} else if decision == InvariantViolated && result.Decision != InvariantUnknown {
				result.Decision = InvariantViolated
			}
		}
	}
	return result, nil
}
