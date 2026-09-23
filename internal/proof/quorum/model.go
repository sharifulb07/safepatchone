// Package quorum evaluates independent verification families. Configuration and
// evidence provenance belong to trusted orchestration, never scanner payloads.
package quorum

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const Schema = "safepatch.quorum-policy.v1"
const MaxSources = 64
const MaxRequirements = 32
const MaxEvidence = 256
const MaxBytes = 128 * 1024

var ErrInvalid = errors.New("invalid evidence quorum")

var capabilityKey = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

func validCapability(s string) bool { return len(s) <= 128 && capabilityKey.MatchString(s) }

type Family string

const (
	Tests           Family = "tests"
	PropertyTesting Family = "property_testing"
	StaticAnalysis  Family = "static_analysis"
	Replay          Family = "replay"
	BusinessGuard   Family = "business_invariant"
	Formal          Family = "formal"
	AIReview        Family = "ai_review"
)

func (f Family) Valid() bool {
	return slices.Contains([]Family{Tests, PropertyTesting, StaticAnalysis, Replay, BusinessGuard, Formal, AIReview}, f)
}

// Source binds an exact producer/type to a family. IndependenceID identifies the
// underlying engine/method, including aliases or wrappers. One such identity and
// one technical capability cannot be assigned to different families. Composition
// must audit these declarations; a content hash cannot authenticate a scanner.
type Source struct {
	ID             string `json:"id"`
	Producer       string `json:"producer"`
	Type           string `json:"type"`
	Family         Family `json:"family"`
	IndependenceID string `json:"independence_id"`
	Capability     string `json:"capability"`
}

type QuorumRequirement struct {
	ID                string   `json:"id"`
	Sources           []string `json:"sources"`
	RequiredFamilies  int      `json:"required_families"`
	MandatoryFamilies []Family `json:"mandatory_families"`
	MinConfidence     float64  `json:"min_confidence"`
	Deterministic     bool     `json:"deterministic"`
}

type PolicyFields struct {
	Schema       string              `json:"schema"`
	ID           string              `json:"id"`
	Version      string              `json:"version"`
	Sources      []Source            `json:"sources"`
	Requirements []QuorumRequirement `json:"requirements"`
}

type QuorumPolicy struct{ canonical string }

func New(f PolicyFields) (QuorumPolicy, error) {
	if f.Schema != Schema || !evidence.ValidIdentifier(f.ID) || !evidence.ValidIdentifier(f.Version) || len(f.Sources) == 0 || len(f.Sources) > MaxSources || len(f.Requirements) == 0 || len(f.Requirements) > MaxRequirements {
		return QuorumPolicy{}, ErrInvalid
	}
	f.Sources = slices.Clone(f.Sources)
	f.Requirements = slices.Clone(f.Requirements)
	sources := map[string]Source{}
	selectors := map[[2]string]bool{}
	identities := map[string]Family{}
	capabilities := map[string]Family{}
	for _, s := range f.Sources {
		key := [2]string{s.Producer, s.Type}
		if !evidence.ValidIdentifier(s.ID) || !evidence.ValidIdentifier(s.Producer) || !validCapability(s.Type) || !s.Family.Valid() || !evidence.ValidIdentifier(s.IndependenceID) || !validCapability(s.Capability) || sources[s.ID].ID != "" || selectors[key] {
			return QuorumPolicy{}, ErrInvalid
		}
		if family := identities[s.IndependenceID]; family != "" && family != s.Family {
			return QuorumPolicy{}, ErrInvalid
		}
		if family := capabilities[s.Capability]; family != "" && family != s.Family {
			return QuorumPolicy{}, ErrInvalid
		}
		sources[s.ID], selectors[key], identities[s.IndependenceID], capabilities[s.Capability] = s, true, s.Family, s.Family
	}
	ids := map[string]bool{}
	for i := range f.Requirements {
		r := &f.Requirements[i]
		if !evidence.ValidIdentifier(r.ID) || ids[r.ID] || len(r.Sources) == 0 || len(r.Sources) > MaxSources || r.RequiredFamilies < 1 || len(r.MandatoryFamilies) > 7 || math.IsNaN(r.MinConfidence) || math.IsInf(r.MinConfidence, 0) || r.MinConfidence < 0 || r.MinConfidence > 1 {
			return QuorumPolicy{}, ErrInvalid
		}
		ids[r.ID] = true
		r.Sources = slices.Clone(r.Sources)
		r.MandatoryFamilies = append([]Family{}, r.MandatoryFamilies...)
		families, seen := map[Family]bool{}, map[string]bool{}
		for _, id := range r.Sources {
			s, ok := sources[id]
			if !ok || seen[id] {
				return QuorumPolicy{}, ErrInvalid
			}
			seen[id], families[s.Family] = true, true
		}
		if r.RequiredFamilies > len(families) {
			return QuorumPolicy{}, ErrInvalid
		}
		mandatory := map[Family]bool{}
		for _, family := range r.MandatoryFamilies {
			if !families[family] || mandatory[family] {
				return QuorumPolicy{}, ErrInvalid
			}
			mandatory[family] = true
		}
		slices.Sort(r.Sources)
		slices.Sort(r.MandatoryFamilies)
	}
	slices.SortFunc(f.Sources, func(a, b Source) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(f.Requirements, func(a, b QuorumRequirement) int { return strings.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return QuorumPolicy{}, ErrInvalid
	}
	return QuorumPolicy{string(raw)}, nil
}

func (p QuorumPolicy) Fields() (PolicyFields, error) {
	var f PolicyFields
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (p QuorumPolicy) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(p.canonical), nil
}
func (*QuorumPolicy) UnmarshalJSON([]byte) error { return ErrInvalid }
func (p QuorumPolicy) Digest() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(p.canonical))
}

// DecodeFields rejects duplicate/unknown/case-aliased/null fields recursively.
func DecodeFields(raw []byte) (PolicyFields, error) {
	clean, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return PolicyFields{}, ErrInvalid
	}
	var f PolicyFields
	if json.Unmarshal(clean, &f) != nil {
		return f, ErrInvalid
	}
	expected, _ := json.Marshal(f)
	normalize := func(b []byte) []byte {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return nil
		}
		if containsNull(v) {
			return nil
		}
		out, _ := json.Marshal(v)
		return out
	}
	actual := normalize(clean)
	if actual == nil || !bytes.Equal(actual, normalize(expected)) {
		return PolicyFields{}, ErrInvalid
	}
	p, err := New(f)
	if err != nil {
		return PolicyFields{}, err
	}
	return p.Fields()
}

func containsNull(v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case map[string]any:
		for _, value := range x {
			if containsNull(value) {
				return true
			}
		}
	case []any:
		for _, value := range x {
			if containsNull(value) {
				return true
			}
		}
	}
	return false
}
