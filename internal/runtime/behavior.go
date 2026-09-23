package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/replay"
)

const MaxMetrics = 64
const MaxRules = 64
const MaxArtifactBytes = 1024 * 1024

var ErrInvalidBehavior = errors.New("invalid behavior artifact or configuration")

// Metric is a trusted projection of a numeric scalar, never executable code.
type Metric struct {
	Name    string `json:"name"`
	Source  string `json:"source"` // INPUT or OUTPUT
	Pointer string `json:"pointer"`
}
type BehaviorCase struct {
	ID       string   `json:"id"`
	Complete bool     `json:"complete"`
	Status   int      `json:"status"`
	Effects  []string `json:"effects"`
	// Canonical rational numbers avoid floating point loss. Missing is unknown.
	Values map[string]string `json:"values"`
}
type BehaviorFields struct {
	Schema          string         `json:"schema"`
	Scope           replay.Scope   `json:"scope"`
	DatasetID       string         `json:"dataset_id"`
	DatasetDigest   string         `json:"dataset_digest"`
	RedactionDigest string         `json:"redaction_digest"`
	CreatedAt       time.Time      `json:"created_at"`
	ExpiresAt       time.Time      `json:"expires_at"`
	Metrics         []Metric       `json:"metrics"`
	Cases           []BehaviorCase `json:"cases"`
}

// BehaviorDNA is immutable and edition independent. It describes observations,
// not their authenticity or a production authorization.
type BehaviorDNA struct{ canonical string }

func (d BehaviorDNA) Digest() string {
	if d.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(d.canonical))
}
func (d BehaviorDNA) MarshalJSON() ([]byte, error) {
	if d.canonical == "" {
		return nil, ErrInvalidBehavior
	}
	return []byte(d.canonical), nil
}
func (d BehaviorDNA) Fields() (BehaviorFields, error) {
	var f BehaviorFields
	if d.canonical == "" || json.Unmarshal([]byte(d.canonical), &f) != nil {
		return f, ErrInvalidBehavior
	}
	return f, nil
}

func normalizeMetrics(metrics []Metric) ([]Metric, error) {
	if len(metrics) == 0 || len(metrics) > MaxMetrics {
		return nil, ErrInvalidBehavior
	}
	metrics = slices.Clone(metrics)
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Name < metrics[j].Name })
	for i, m := range metrics {
		if !evidence.ValidIdentifier(m.Name) || (i > 0 && m.Name == metrics[i-1].Name) || (m.Source != "INPUT" && m.Source != "OUTPUT") || len(m.Pointer) > 256 || !strings.HasPrefix(m.Pointer, "/") {
			return nil, ErrInvalidBehavior
		}
		parts := strings.Split(m.Pointer[1:], "/")
		if len(parts) > 8 {
			return nil, ErrInvalidBehavior
		}
		for _, p := range parts {
			if !evidence.ValidIdentifier(p) {
				return nil, ErrInvalidBehavior
			}
		}
	}
	return metrics, nil
}

func ValidateBehaviorMetrics(metrics []Metric) error {
	_, err := normalizeMetrics(metrics)
	return err
}

var decimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]{1,2})?$`)
var rational = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(/[1-9][0-9]*)?$`)

func number(s string) (*big.Rat, bool) {
	if len(s) > 96 || !decimal.MatchString(s) {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}
func scalar(raw []byte, pointer string) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var v any
	if decoder.Decode(&v) != nil {
		return "", false
	}
	for _, p := range strings.Split(pointer[1:], "/") {
		obj, ok := v.(map[string]any)
		if !ok {
			return "", false
		}
		v, ok = obj[p]
		if !ok {
			return "", false
		}
	}
	n, ok := v.(json.Number)
	if !ok {
		return "", false
	}
	r, ok := number(string(n))
	if !ok {
		return "", false
	}
	return r.RatString(), true
}

// BuildBehaviorDNA consumes the existing privacy-redacted ReplayDataset. Its
// Expected observations are recorded baseline observations, not replay outcomes.
func BuildBehaviorDNA(ctx context.Context, d replay.ReplayDataset, metrics []Metric) (BehaviorDNA, error) {
	if ctx == nil {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	if err := ctx.Err(); err != nil {
		return BehaviorDNA{}, err
	}
	metrics, err := normalizeMetrics(metrics)
	if err != nil {
		return BehaviorDNA{}, err
	}
	source, err := d.Fields()
	if err != nil {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	f := BehaviorFields{Schema: "safepatch.behaviordna.v1", Scope: source.Scope, DatasetID: source.ID, DatasetDigest: d.Digest(), RedactionDigest: source.RedactionDigest, CreatedAt: source.CreatedAt, ExpiresAt: source.ExpiresAt, Metrics: metrics, Cases: []BehaviorCase{}}
	for _, record := range source.Records {
		if err := ctx.Err(); err != nil {
			return BehaviorDNA{}, err
		}
		c := BehaviorCase{ID: record.ID, Complete: record.Expected.Complete, Status: record.Expected.Status, Effects: append([]string{}, record.Expected.Effects...), Values: map[string]string{}}
		for _, m := range metrics {
			raw := record.Input
			if m.Source == "OUTPUT" {
				raw = record.Expected.Output
			}
			if n, ok := scalar(raw, m.Pointer); ok {
				c.Values[m.Name] = n
			}
		}
		f.Cases = append(f.Cases, c)
	}
	return sealBehavior(f)
}
func sealBehavior(f BehaviorFields) (BehaviorDNA, error) {
	if !evidence.ValidIdentifier(f.DatasetID) {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	metrics, err := normalizeMetrics(f.Metrics)
	if err != nil || !slices.Equal(metrics, f.Metrics) || f.Schema != "safepatch.behaviordna.v1" || f.Scope.Validate() != nil || !digestValid(f.DatasetDigest) || !digestValid(f.RedactionDigest) || f.CreatedAt.IsZero() || f.CreatedAt.Year() < 1 || f.ExpiresAt.Year() > 9999 || !f.ExpiresAt.After(f.CreatedAt) || f.ExpiresAt.Sub(f.CreatedAt) > 366*24*time.Hour || len(f.Cases) == 0 || len(f.Cases) > replay.MaxRecords {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	names := map[string]bool{}
	for _, m := range metrics {
		names[m.Name] = true
	}
	ids := map[string]bool{}
	for _, c := range f.Cases {
		if !evidence.ValidIdentifier(c.ID) || ids[c.ID] || c.Status < 0 || c.Status > 599 || (c.Complete && c.Status == 0) || c.Values == nil || c.Effects == nil || len(c.Effects) > 32 || len(c.Values) > len(metrics) {
			return BehaviorDNA{}, ErrInvalidBehavior
		}
		ids[c.ID] = true
		for _, id := range c.Effects {
			if !evidence.ValidIdentifier(id) {
				return BehaviorDNA{}, ErrInvalidBehavior
			}
		}
		for name, value := range c.Values {
			if !names[name] || len(value) > 400 || !rational.MatchString(value) {
				return BehaviorDNA{}, ErrInvalidBehavior
			}
			v, ok := new(big.Rat).SetString(value)
			if !ok || v.RatString() != value {
				return BehaviorDNA{}, ErrInvalidBehavior
			}
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxArtifactBytes {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	return BehaviorDNA{string(raw)}, nil
}
func digestValid(s string) bool {
	return (evidence.OriginalReference{SHA256: s, Bytes: 0}).Validate() == nil
}

// strictObject rejects duplicate keys, aliases, unknown fields and nulls where
// the schema requires a value. Canonical ordering itself is not required.
func strictObject(raw []byte, target any) error {
	clean, err := jsonobject.Normalize(raw, MaxArtifactBytes)
	if err != nil {
		return ErrInvalidBehavior
	}
	if json.Unmarshal(clean, target) != nil {
		return ErrInvalidBehavior
	}
	encoded, err := json.Marshal(target)
	if err != nil {
		return ErrInvalidBehavior
	}
	normalize := func(b []byte) []byte {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return nil
		}
		out, _ := json.Marshal(v)
		return out
	}
	if !bytes.Equal(normalize(clean), normalize(encoded)) {
		return ErrInvalidBehavior
	}
	return nil
}
func DecodeBehaviorDNA(raw []byte) (BehaviorDNA, error) {
	var f BehaviorFields
	if strictObject(raw, &f) != nil {
		return BehaviorDNA{}, ErrInvalidBehavior
	}
	return sealBehavior(f)
}

// CompareBehavior requires the same tenant, privacy policy and metric projection.
// Missing cases/measurements and incomplete observations dominate differences.
func CompareBehavior(ctx context.Context, a, b BehaviorDNA) (replay.Decision, error) {
	if ctx == nil {
		return replay.Unknown, ErrInvalidBehavior
	}
	if err := ctx.Err(); err != nil {
		return replay.Unknown, err
	}
	left, e1 := a.Fields()
	right, e2 := b.Fields()
	if e1 != nil || e2 != nil || left.Scope != right.Scope || left.RedactionDigest != right.RedactionDigest || !slices.Equal(left.Metrics, right.Metrics) {
		return replay.Unknown, ErrInvalidBehavior
	}
	index := map[string]BehaviorCase{}
	for _, c := range right.Cases {
		index[c.ID] = c
	}
	decision := replay.Match
	if len(left.Cases) != len(right.Cases) {
		decision = replay.Unknown
	}
	for _, c := range left.Cases {
		if err := ctx.Err(); err != nil {
			return replay.Unknown, err
		}
		other, ok := index[c.ID]
		if !ok || !c.Complete || !other.Complete || len(c.Values) != len(left.Metrics) || len(other.Values) != len(right.Metrics) {
			decision = replay.Unknown
			continue
		}
		equal := c.Status == other.Status && slices.Equal(c.Effects, other.Effects)
		for k, v := range c.Values {
			equal = equal && other.Values[k] == v
		}
		if !equal && decision != replay.Unknown {
			decision = replay.Different
		}
	}
	return decision, nil
}
