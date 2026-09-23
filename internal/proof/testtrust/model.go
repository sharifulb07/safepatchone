// Package testtrust compares bounded test evidence using one edition-independent schema.
package testtrust

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"slices"
	"sort"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const MaxBytes = 2 * 1024 * 1024
const MaxTests = 1024
const MaxAssertions = 4096

var ErrInvalid = errors.New("invalid TestTrust evidence")

type Assertion struct {
	ID              string `json:"id"`
	SubjectDigest   string `json:"subject_digest"`
	PredicateDigest string `json:"predicate_digest"`
	Relation        string `json:"relation"` // EQ NE GT GE LT LE or UNKNOWN
	Bound           string `json:"bound"`    // exact decimal; empty for UNKNOWN
}
type Test struct {
	ID         string                  `json:"id"`
	Source     evidence.SourceLocation `json:"source"`
	Complete   bool                    `json:"complete"`
	Skip       string                  `json:"skip"`     // ENABLED SKIPPED CONDITIONAL UNKNOWN
	Negative   string                  `json:"negative"` // NEGATIVE POSITIVE UNKNOWN
	Assertions []Assertion             `json:"assertions"`
}
type Coverage struct {
	Known       bool   `json:"known"`
	BasisDigest string `json:"basis_digest"` // trusted instrumentation/cohort configuration
	Covered     int64  `json:"covered"`
	Total       int64  `json:"total"`
}
type Fields struct {
	Schema          string           `json:"schema"`
	OrganizationID  string           `json:"organization_id"`
	Subject         evidence.Subject `json:"subject"`
	Producer        string           `json:"producer"`
	ProducerVersion string           `json:"producer_version"`
	SourceDigest    string           `json:"source_digest"`
	Complete        bool             `json:"complete"` // full test discovery, not correctness
	Tests           []Test           `json:"tests"`
	Coverage        Coverage         `json:"coverage"`
}
type Snapshot struct{ canonical string }

func (s Snapshot) Digest() string {
	if s.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(s.canonical))
}
func (s Snapshot) MarshalJSON() ([]byte, error) {
	if s.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(s.canonical), nil
}
func (s Snapshot) Fields() (Fields, error) {
	var f Fields
	if s.canonical == "" || json.Unmarshal([]byte(s.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (*Snapshot) UnmarshalJSON([]byte) error { return ErrInvalid }

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

func number(s string) (*big.Rat, bool) {
	if len(s) > 96 || !decimal.MatchString(s) {
		return nil, false
	}
	return new(big.Rat).SetString(s)
}

func ValidNumericBound(s string) bool { _, ok := number(s); return ok }
func digest(s string) bool {
	return (evidence.OriginalReference{SHA256: s, Bytes: 0}).Validate() == nil
}
func validLocation(s evidence.SourceLocation) bool {
	return evidence.ValidPath(s.Path) && len(s.Path) <= 512 && s.StartLine >= 0 && s.StartColumn >= 0 && s.EndLine >= 0 && s.EndColumn >= 0 && !(s.StartColumn > 0 && s.StartLine == 0) && !(s.EndLine > 0 && (s.StartLine == 0 || s.EndLine < s.StartLine)) && !(s.EndColumn > 0 && s.EndLine == 0) && !(s.EndLine == s.StartLine && s.EndColumn > 0 && (s.StartColumn == 0 || s.EndColumn < s.StartColumn))
}
func New(f Fields) (Snapshot, error) {
	if f.Schema != "safepatch.testtrust-snapshot.v1" || !tenant.ValidID(f.OrganizationID) || f.Subject.Validate() != nil || !evidence.ValidIdentifier(f.Producer) || !evidence.ValidIdentifier(f.ProducerVersion) || !digest(f.SourceDigest) || len(f.Tests) > MaxTests || f.Tests == nil {
		return Snapshot{}, ErrInvalid
	}
	c := f.Coverage
	if c.Known {
		if !digest(c.BasisDigest) || c.Total < 1 || c.Total > 1_000_000_000 || c.Covered < 0 || c.Covered > c.Total {
			return Snapshot{}, ErrInvalid
		}
	} else if c.BasisDigest != "" || c.Total != 0 || c.Covered != 0 {
		return Snapshot{}, ErrInvalid
	}
	// Copy before sorting so construction never mutates caller-owned slices.
	f.Tests = slices.Clone(f.Tests)
	sort.Slice(f.Tests, func(i, j int) bool { return f.Tests[i].ID < f.Tests[j].ID })
	count := 0
	for i := range f.Tests {
		t := &f.Tests[i]
		if !evidence.ValidIdentifier(t.ID) || (i > 0 && t.ID == f.Tests[i-1].ID) || !validLocation(t.Source) || !slices.Contains([]string{"ENABLED", "SKIPPED", "CONDITIONAL", "UNKNOWN"}, t.Skip) || !slices.Contains([]string{"NEGATIVE", "POSITIVE", "UNKNOWN"}, t.Negative) || t.Assertions == nil {
			return Snapshot{}, ErrInvalid
		}
		count += len(t.Assertions)
		if count > MaxAssertions {
			return Snapshot{}, ErrInvalid
		}
		t.Assertions = slices.Clone(t.Assertions)
		sort.Slice(t.Assertions, func(i, j int) bool { return t.Assertions[i].ID < t.Assertions[j].ID })
		for j, a := range t.Assertions {
			if !evidence.ValidIdentifier(a.ID) || (j > 0 && a.ID == t.Assertions[j-1].ID) || !digest(a.SubjectDigest) || !digest(a.PredicateDigest) {
				return Snapshot{}, ErrInvalid
			}
			if a.Relation == "UNKNOWN" {
				if a.Bound != "" {
					return Snapshot{}, ErrInvalid
				}
			} else {
				if !slices.Contains([]string{"EQ", "NE", "GT", "GE", "LT", "LE"}, a.Relation) {
					return Snapshot{}, ErrInvalid
				}
				if _, ok := number(a.Bound); !ok {
					return Snapshot{}, ErrInvalid
				}
			}
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return Snapshot{}, ErrInvalid
	}
	return Snapshot{string(raw)}, nil
}
func Decode(raw []byte) (Snapshot, error) {
	clean, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return Snapshot{}, ErrInvalid
	}
	var f Fields
	if json.Unmarshal(clean, &f) != nil {
		return Snapshot{}, ErrInvalid
	}
	expected, _ := json.Marshal(f)
	canonical := func(b []byte) []byte {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return nil
		}
		out, _ := json.Marshal(v)
		return out
	}
	if !bytes.Equal(canonical(clean), canonical(expected)) {
		return Snapshot{}, ErrInvalid
	}
	return New(f)
}
