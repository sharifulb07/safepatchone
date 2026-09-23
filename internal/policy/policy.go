package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/quorum"
	"safepatch/internal/tenant"
)

const (
	Schema          = "safepatch.policy.v1"
	MaxPolicyBytes  = 256 * 1024
	MaxRules        = 128
	MaxSelectors    = 64
	MaxRequirements = 16
	MaxEvidence     = 256
	MaxInputBytes   = 16 * 1024 * 1024
)

var (
	ErrInvalidPolicy   = errors.New("invalid policy document")
	ErrInvalidInput    = errors.New("invalid policy evaluation input")
	ErrImmutablePolicy = errors.New("decode immutable policy using Decode")
	keyPattern         = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
)

type Criticality string

const (
	CriticalityUnknown  Criticality = "UNKNOWN"
	CriticalityLow      Criticality = "LOW"
	CriticalityMedium   Criticality = "MEDIUM"
	CriticalityHigh     Criticality = "HIGH"
	CriticalityCritical Criticality = "CRITICAL"
)

func (c Criticality) valid() bool {
	return slices.Contains([]Criticality{CriticalityUnknown, CriticalityLow, CriticalityMedium, CriticalityHigh, CriticalityCritical}, c)
}

// Document is a mutable construction DTO. New/Decode seal an independent copy.
// Version identifies policy configuration, not the product release or edition.
type Document struct {
	Schema   string `json:"schema"`
	PolicyID string `json:"policy_id"`
	TenantID string `json:"tenant_id"`
	Version  string `json:"version"`
	Rules    []Rule `json:"rules"`
}
type Rule struct {
	ID      string       `json:"id"`
	When    Selector     `json:"when"`
	Require Requirements `json:"require"`
}
type Selector struct {
	RepositoryIDs []string      `json:"repository_ids,omitempty"`
	Criticalities []Criticality `json:"criticalities,omitempty"`
}
type Requirements struct {
	Quorum        *quorum.PolicyFields  `json:"quorum,omitempty"`
	Evidence      []EvidenceRequirement `json:"evidence,omitempty"`
	MaxRiskScore  *int                  `json:"max_risk_score,omitempty"`
	MinProofLevel *uint32               `json:"min_proof_level,omitempty"`
}

// All matching observations must PASS. Unknown, unavailable or missing evidence
// cannot satisfy a requirement. No payload, prompt or scanner message is code.
type EvidenceRequirement struct {
	Producer      string  `json:"producer"`
	Type          string  `json:"type"`
	MinConfidence float64 `json:"min_confidence,omitempty"`
	Deterministic bool    `json:"deterministic,omitempty"`
}

// Policy has no mutators and holds only private canonical JSON. It can be shared
// concurrently. A hash identifies configuration; it is not an approval/signature.
type Policy struct{ canonical string }

func New(d Document) (Policy, error) {
	if err := validate(d); err != nil {
		return Policy{}, err
	}
	d.Rules = slices.Clone(d.Rules)
	for i := range d.Rules {
		if d.Rules[i].Require.Quorum != nil {
			p, _ := quorum.New(*d.Rules[i].Require.Quorum)
			f, _ := p.Fields()
			d.Rules[i].Require.Quorum = &f
		}
	}
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > MaxPolicyBytes {
		return Policy{}, ErrInvalidPolicy
	}
	return Policy{canonical: string(raw)}, nil
}
func (p Policy) Document() (Document, error) {
	var d Document
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &d) != nil {
		return d, ErrInvalidPolicy
	}
	return d, nil
}
func (p Policy) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalidPolicy
	}
	return []byte(p.canonical), nil
}
func (*Policy) UnmarshalJSON([]byte) error { return ErrImmutablePolicy }
func (p Policy) Hash() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(Schema + "\n" + p.canonical))
}

func validate(d Document) error {
	if d.Schema != Schema || !evidence.ValidIdentifier(d.PolicyID) || !tenant.ValidID(d.TenantID) || !evidence.ValidIdentifier(d.Version) || len(d.Rules) == 0 || len(d.Rules) > MaxRules {
		return ErrInvalidPolicy
	}
	seen := map[string]bool{}
	for _, r := range d.Rules {
		if !evidence.ValidIdentifier(r.ID) || seen[r.ID] || len(r.When.RepositoryIDs) > MaxSelectors || len(r.When.Criticalities) > 4 {
			return ErrInvalidPolicy
		}
		seen[r.ID] = true
		repos := map[string]bool{}
		for _, id := range r.When.RepositoryIDs {
			if !tenant.ValidID(id) || repos[id] {
				return ErrInvalidPolicy
			}
			repos[id] = true
		}
		criticalities := map[Criticality]bool{}
		for _, c := range r.When.Criticalities {
			if !c.valid() || c == CriticalityUnknown || criticalities[c] {
				return ErrInvalidPolicy
			}
			criticalities[c] = true
		}
		req := r.Require
		if len(req.Evidence) > MaxRequirements || len(req.Evidence) == 0 && req.MaxRiskScore == nil && req.MinProofLevel == nil && req.Quorum == nil || req.MaxRiskScore != nil && (*req.MaxRiskScore < 0 || *req.MaxRiskScore > 100) {
			return ErrInvalidPolicy
		}
		if req.Quorum != nil {
			if _, err := quorum.New(*req.Quorum); err != nil {
				return ErrInvalidPolicy
			}
		}
		sources := map[[2]string]bool{}
		for _, e := range req.Evidence {
			key := [2]string{e.Producer, e.Type}
			if !evidence.ValidIdentifier(e.Producer) || len(e.Type) > 128 || !keyPattern.MatchString(e.Type) || math.IsNaN(e.MinConfidence) || math.IsInf(e.MinConfidence, 0) || e.MinConfidence < 0 || e.MinConfidence > 1 || sources[key] {
				return ErrInvalidPolicy
			}
			sources[key] = true
		}
	}
	return nil
}

// Decode rejects duplicate keys, unknown fields, case aliases, null fields and
// executable expressions. Optional fields are omitted rather than set to null.
func Decode(raw []byte) (Policy, error) {
	checked, err := jsonobject.Normalize(raw, MaxPolicyBytes)
	if err != nil || len(raw) == 0 {
		return Policy{}, ErrInvalidPolicy
	}
	fields, err := object(checked, "schema", "policy_id", "tenant_id", "version", "rules")
	if err != nil || len(fields) != 5 {
		return Policy{}, ErrInvalidPolicy
	}
	var rules []json.RawMessage
	if json.Unmarshal(fields["rules"], &rules) != nil || len(rules) > MaxRules {
		return Policy{}, ErrInvalidPolicy
	}
	for _, rawRule := range rules {
		r, err := object(rawRule, "id", "when", "require")
		if err != nil || len(r) != 3 {
			return Policy{}, ErrInvalidPolicy
		}
		if _, err := object(r["when"], "repository_ids", "criticalities"); err != nil {
			return Policy{}, err
		}
		req, err := object(r["require"], "evidence", "max_risk_score", "min_proof_level", "quorum")
		if err != nil {
			return Policy{}, err
		}
		if rawQuorum, ok := req["quorum"]; ok {
			if _, err := quorum.DecodeFields(rawQuorum); err != nil {
				return Policy{}, ErrInvalidPolicy
			}
		}
		if rawEvidence, ok := req["evidence"]; ok {
			var entries []json.RawMessage
			if json.Unmarshal(rawEvidence, &entries) != nil || len(entries) > MaxRequirements {
				return Policy{}, ErrInvalidPolicy
			}
			for _, entry := range entries {
				if _, err := object(entry, "producer", "type", "min_confidence", "deterministic"); err != nil {
					return Policy{}, err
				}
			}
		}
	}
	var d Document
	if json.Unmarshal(checked, &d) != nil {
		return Policy{}, ErrInvalidPolicy
	}
	return New(d)
}
func object(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrInvalidPolicy
	}
	for k, v := range fields {
		if !slices.Contains(keys, k) || bytes.Equal(v, []byte("null")) {
			return nil, ErrInvalidPolicy
		}
	}
	return fields, nil
}
