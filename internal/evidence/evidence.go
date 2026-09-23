package evidence

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"safepatch/internal/domain/jsonobject"
)

const (
	EvidenceSchema     = "safepatch.evidence.v1"
	MaxPayloadBytes    = 512 * 1024
	MaxSourceLocations = 1024
	MaxArtifacts       = 256
	MaxInputHashes     = 256
)

var (
	ErrImmutableEvidence = errors.New("decode immutable evidence using DecodeEvidence")
	ErrEvidenceHash      = errors.New("evidence content hash mismatch")
	evidenceKey          = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	producerVersion      = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

// SourceLocation addresses only repository-relative source. Zero positions mean
// a whole file. Positive coordinates are one-based; end coordinates are inclusive.
type SourceLocation struct {
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

// Artifact is a logical, tenant-scoped immutable object reference, never a URL
// or filesystem path. Its resolver must authorize access and verify bytes/hash.
// Unlike bounded process capture, a stored artifact can exceed 16 MiB.
type Artifact struct {
	ArtifactID  string `json:"artifact_id"`
	ContentHash string `json:"content_hash"`
	SizeBytes   int64  `json:"size_bytes"`
}

// EvidenceFields is a mutable construction/inspection DTO, not sealed evidence.
// All identities, time, determinism and confidence come from trusted producers
// or orchestration. None are inferred from an edition, scanner text or an LLM.
type EvidenceFields struct {
	EvidenceID      string            `json:"evidence_id"`
	TenantID        string            `json:"tenant_id"`
	ChangeID        string            `json:"change_id"`
	RunID           string            `json:"run_id"`
	Producer        string            `json:"producer"`
	ProducerVersion string            `json:"producer_version"`
	Category        string            `json:"category"`
	Type            string            `json:"type"`
	Deterministic   bool              `json:"deterministic"`
	Status          Status            `json:"status"`
	Severity        Severity          `json:"severity"`
	Confidence      float64           `json:"confidence"`
	Summary         string            `json:"summary"`
	Payload         json.RawMessage   `json:"payload"`
	SourceLocations []SourceLocation  `json:"source_locations"`
	Artifacts       []Artifact        `json:"artifacts"`
	InputHashes     map[string]string `json:"input_hashes"`
	ContentHash     string            `json:"content_hash"`
	CreatedAt       time.Time         `json:"created_at"`
}

// Evidence owns canonical serialized bytes. There are no setters or exported
// mutable fields. Construction and reads copy data, so sharing a value across
// goroutines is safe. Immutability is not authenticity or access authorization.
type Evidence struct{ canonical []byte }

func NewEvidence(fields EvidenceFields) (Evidence, error) {
	if fields.ContentHash != "" {
		return Evidence{}, ErrInvalidEvidence
	}
	return sealEvidence(fields)
}

func sealEvidence(fields EvidenceFields) (Evidence, error) {
	f, err := canonicalFields(fields)
	if err != nil {
		return Evidence{}, err
	}
	f.ContentHash = ""
	unsigned, err := json.Marshal(f)
	if err != nil || len(unsigned) > MaxEvidenceBytes {
		return Evidence{}, ErrInvalidEvidence
	}
	f.ContentHash = Digest(append([]byte(EvidenceSchema+"\n"), unsigned...))
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxEvidenceBytes {
		return Evidence{}, ErrInvalidEvidence
	}
	return Evidence{canonical: raw}, nil
}

// Fields returns a fully independent snapshot, including every nested value.
func (e Evidence) Fields() (EvidenceFields, error) {
	if len(e.canonical) == 0 {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	var f EvidenceFields
	if json.Unmarshal(e.canonical, &f) != nil {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	return f, nil
}
func (e Evidence) CanonicalJSON() ([]byte, error) {
	if len(e.canonical) == 0 {
		return nil, ErrInvalidEvidence
	}
	return slices.Clone(e.canonical), nil
}
func (e Evidence) MarshalJSON() ([]byte, error) { return e.CanonicalJSON() }

// Decode explicitly rather than overwriting an existing sealed value. This
// also prevents encoding/json silently ignoring the private representation.
func (*Evidence) UnmarshalJSON([]byte) error { return ErrImmutableEvidence }

func canonicalFields(f EvidenceFields) (EvidenceFields, error) {
	for _, id := range []string{f.EvidenceID, f.TenantID, f.ChangeID, f.RunID, f.Producer} {
		if !ValidIdentifier(id) {
			return EvidenceFields{}, ErrInvalidEvidence
		}
	}
	if len(f.ProducerVersion) > 128 || !producerVersion.MatchString(f.ProducerVersion) || !validEvidenceKey(f.Category) || !validEvidenceKey(f.Type) || !f.Status.Valid() || math.IsNaN(f.Confidence) || math.IsInf(f.Confidence, 0) || f.Confidence < 0 || f.Confidence > 1 || !ValidText(f.Summary, 4096) {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	switch f.Severity {
	case SeverityInfo, SeverityWarning, SeverityError:
	default:
		return EvidenceFields{}, ErrInvalidEvidence
	}
	if f.Status == StatusPass && f.Severity != SeverityInfo || f.Status == StatusWarn && f.Severity == SeverityError {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	if f.CreatedAt.IsZero() || f.CreatedAt.UTC().Year() < 1 || f.CreatedAt.UTC().Year() > 9999 {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	f.CreatedAt = f.CreatedAt.UTC().Round(0)
	if f.Confidence == 0 {
		f.Confidence = 0
	} // canonical positive zero
	if len(f.SourceLocations) > MaxSourceLocations || len(f.Artifacts) > MaxArtifacts || len(f.InputHashes) > MaxInputHashes {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	raw, err := jsonobject.Normalize(f.Payload, MaxPayloadBytes)
	if err != nil {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	// Sort object keys recursively without converting JSON numbers to float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload any
	if decoder.Decode(&payload) != nil {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	f.Payload, err = json.Marshal(payload)
	if err != nil || len(f.Payload) > MaxPayloadBytes {
		return EvidenceFields{}, ErrInvalidEvidence
	}
	f.SourceLocations = append([]SourceLocation{}, f.SourceLocations...)
	for _, s := range f.SourceLocations {
		if !ValidPath(s.Path) || s.StartLine < 0 || s.StartColumn < 0 || s.EndLine < 0 || s.EndColumn < 0 || s.StartColumn > 0 && s.StartLine == 0 || s.EndLine > 0 && (s.StartLine == 0 || s.EndLine < s.StartLine) || s.EndColumn > 0 && s.EndLine == 0 || s.EndLine == s.StartLine && s.EndColumn > 0 && (s.StartColumn == 0 || s.EndColumn < s.StartColumn) {
			return EvidenceFields{}, ErrInvalidEvidence
		}
	}
	slices.SortFunc(f.SourceLocations, func(a, b SourceLocation) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		for _, p := range [][2]int{{a.StartLine, b.StartLine}, {a.StartColumn, b.StartColumn}, {a.EndLine, b.EndLine}, {a.EndColumn, b.EndColumn}} {
			if p[0] < p[1] {
				return -1
			}
			if p[0] > p[1] {
				return 1
			}
		}
		return 0
	})
	f.SourceLocations = slices.Compact(f.SourceLocations)
	f.Artifacts = append([]Artifact{}, f.Artifacts...)
	seen := map[string]bool{}
	for _, a := range f.Artifacts {
		if !ValidIdentifier(a.ArtifactID) || seen[a.ArtifactID] || !validSHA256(a.ContentHash) || a.SizeBytes < 0 {
			return EvidenceFields{}, ErrInvalidEvidence
		}
		seen[a.ArtifactID] = true
	}
	slices.SortFunc(f.Artifacts, func(a, b Artifact) int { return strings.Compare(a.ArtifactID, b.ArtifactID) })
	hashes := make(map[string]string, len(f.InputHashes))
	for key, hash := range f.InputHashes {
		if !ValidIdentifier(key) || !validSHA256(hash) {
			return EvidenceFields{}, ErrInvalidEvidence
		}
		hashes[key] = hash
	}
	f.InputHashes = hashes
	return f, nil
}
func validEvidenceKey(s string) bool { return len(s) <= 128 && evidenceKey.MatchString(s) }
func validSHA256(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32
}

// DecodeEvidence accepts only the complete schema, validates all nested fields
// and verifies the hash after canonicalization. A recomputed malicious hash is
// not prevented by this integrity check; trusted provenance remains essential.
func DecodeEvidence(raw []byte) (Evidence, error) {
	if len(raw) == 0 {
		return Evidence{}, ErrInvalidEvidence
	}
	checked, err := jsonobject.Normalize(raw, MaxEvidenceBytes)
	if err != nil {
		return Evidence{}, ErrInvalidEvidence
	}
	keys := []string{"evidence_id", "tenant_id", "change_id", "run_id", "producer", "producer_version", "category", "type", "deterministic", "status", "severity", "confidence", "summary", "payload", "source_locations", "artifacts", "input_hashes", "content_hash", "created_at"}
	fields, err := exactFields(checked, keys...)
	if err != nil || len(fields) != len(keys) {
		return Evidence{}, ErrInvalidEvidence
	}
	for _, key := range keys {
		if bytes.Equal(fields[key], []byte("null")) {
			return Evidence{}, ErrInvalidEvidence
		}
	}
	if _, err := jsonobject.Normalize(fields["payload"], MaxPayloadBytes); err != nil {
		return Evidence{}, ErrInvalidEvidence
	}
	for _, spec := range []struct {
		key  string
		keys []string
		max  int
	}{{"source_locations", []string{"path", "start_line", "start_column", "end_line", "end_column"}, MaxSourceLocations}, {"artifacts", []string{"artifact_id", "content_hash", "size_bytes"}, MaxArtifacts}} {
		var items []json.RawMessage
		if json.Unmarshal(fields[spec.key], &items) != nil || items == nil || len(items) > spec.max {
			return Evidence{}, ErrInvalidEvidence
		}
		for _, item := range items {
			nested, err := exactFields(item, spec.keys...)
			if err != nil || len(nested) != len(spec.keys) {
				return Evidence{}, ErrInvalidEvidence
			}
			for _, v := range nested {
				if bytes.Equal(v, []byte("null")) {
					return Evidence{}, ErrInvalidEvidence
				}
			}
		}
	}
	var f EvidenceFields
	if json.Unmarshal(checked, &f) != nil || !validSHA256(f.ContentHash) {
		return Evidence{}, ErrInvalidEvidence
	}
	expected := f.ContentHash
	result, err := sealEvidence(f)
	if err != nil {
		return Evidence{}, err
	}
	sealed, _ := result.Fields()
	if sealed.ContentHash != expected {
		return Evidence{}, ErrEvidenceHash
	}
	return result, nil
}
