package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"safepatch/internal/domain/jsonobject"
)

type Status string

const (
	StatusPass         Status = "PASS"
	StatusFail         Status = "FAIL"
	StatusWarn         Status = "WARN"
	StatusError        Status = "ERROR"
	StatusSkipped      Status = "SKIPPED"
	StatusUnavailable  Status = "UNAVAILABLE"
	StatusUnknown      Status = "UNKNOWN"
	VerificationSchema        = "safepatch.verification.v1"
	MaxFindings               = 1024
	MaxExecutions             = 16
	MaxEvidenceBytes          = 1024 * 1024
)

func (s Status) Valid() bool {
	switch s {
	case StatusPass, StatusFail, StatusWarn, StatusError, StatusSkipped, StatusUnavailable, StatusUnknown:
		return true
	}
	return false
}

var ErrInvalidEvidence = errors.New("invalid normalized verification evidence")
var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func ValidIdentifier(value string) bool { return len(value) <= 128 && identifier.MatchString(value) }
func ValidText(value string, bound int) bool {
	if value == "" || len(value) > bound || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func ValidPath(value string) bool {
	if !ValidText(value, 4096) || strings.ContainsAny(value, "\\:") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}

type Subject struct {
	RepositoryID string `json:"repository_id"`
	Revision     string `json:"revision"` // trusted immutable source identity, not a branch name
}

func (s Subject) Validate() error {
	if !ValidIdentifier(s.RepositoryID) || !ValidIdentifier(s.Revision) {
		return ErrInvalidEvidence
	}
	return nil
}

type Severity string

const (
	SeverityInfo    Severity = "INFO"
	SeverityWarning Severity = "WARNING"
	SeverityError   Severity = "ERROR"
)

type Finding struct {
	RuleID   string   `json:"rule_id"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Column   int      `json:"column,omitempty"`
}

// Report is the only part supplied by a verifier normalizer. Identity and
// execution provenance are attached by the SDK, never read from scanner output.
type Report struct {
	Status   Status    `json:"status"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

// DecodeReport is optional for verifiers whose tools emit this exact report
// schema. Other formats need a dedicated parser; never infer PASS from text or
// exit code alone. Duplicate keys, unknown fields and malformed JSON fail closed.
func DecodeReport(raw []byte) (Report, error) {
	normalized, err := jsonobject.Normalize(raw, MaxEvidenceBytes)
	if err != nil {
		return Report{}, ErrInvalidEvidence
	}
	// encoding/json matches struct fields case-insensitively. Enforce exact
	// schema keys first so status/STATUS cannot bypass duplicate-key checks.
	fields, err := exactFields(normalized, "status", "summary", "findings")
	if err != nil {
		return Report{}, err
	}
	if findings, present := fields["findings"]; present {
		var items []json.RawMessage
		if json.Unmarshal(findings, &items) != nil || len(items) > MaxFindings {
			return Report{}, ErrInvalidEvidence
		}
		for _, item := range items {
			if _, err := exactFields(item, "rule_id", "severity", "message", "path", "line", "column"); err != nil {
				return Report{}, err
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	var report Report
	if decoder.Decode(&report) != nil {
		return Report{}, ErrInvalidEvidence
	}
	return report.Canonical()
}

func exactFields(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrInvalidEvidence
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return nil, ErrInvalidEvidence
		}
	}
	return fields, nil
}

func (r Report) Canonical() (Report, error) {
	if !r.Status.Valid() || !ValidText(r.Summary, 4096) || len(r.Findings) > MaxFindings {
		return Report{}, ErrInvalidEvidence
	}
	r.Findings = append([]Finding{}, r.Findings...)
	size := len(r.Summary)
	for _, f := range r.Findings {
		if !ValidIdentifier(f.RuleID) || !ValidText(f.Message, 4096) || f.Line < 0 || f.Column < 0 || (f.Path != "" && !ValidPath(f.Path)) || (f.Path == "" && (f.Line != 0 || f.Column != 0)) || (f.Column > 0 && f.Line == 0) {
			return Report{}, ErrInvalidEvidence
		}
		switch f.Severity {
		case SeverityInfo, SeverityWarning, SeverityError:
		default:
			return Report{}, ErrInvalidEvidence
		}
		if r.Status == StatusPass && f.Severity != SeverityInfo || r.Status == StatusWarn && f.Severity == SeverityError {
			return Report{}, ErrInvalidEvidence
		}
		size += len(f.RuleID) + len(f.Message) + len(f.Path) + 128
		if size > MaxEvidenceBytes {
			return Report{}, ErrInvalidEvidence
		}
	}
	slices.SortFunc(r.Findings, func(a, b Finding) int {
		for _, pair := range [][2]string{{a.Path, b.Path}, {a.RuleID, b.RuleID}, {string(a.Severity), string(b.Severity)}, {a.Message, b.Message}} {
			if c := strings.Compare(pair[0], pair[1]); c != 0 {
				return c
			}
		}
		if a.Line < b.Line {
			return -1
		}
		if a.Line > b.Line {
			return 1
		}
		if a.Column < b.Column {
			return -1
		}
		if a.Column > b.Column {
			return 1
		}
		return 0
	})
	r.Findings = slices.Compact(r.Findings)
	return r, nil
}

// Execution contains bounded provenance, never raw output, environment, argv,
// host paths, credentials, or arbitrary artifact URLs. Digests are integrity
// identifiers, not signatures or attestations of trustworthy scanner content.
type Execution struct {
	StepID       string `json:"step_id"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exit_code"`
	StdoutSHA256 string `json:"stdout_sha256"`
	StderrSHA256 string `json:"stderr_sha256"`
	Sandboxed    bool   `json:"sandboxed"`
}
type Record struct {
	Schema          string      `json:"schema"`
	VerifierID      string      `json:"verifier_id"`
	VerifierVersion string      `json:"verifier_version"`
	Capability      string      `json:"capability"`
	Subject         Subject     `json:"subject"`
	Report          Report      `json:"report"`
	Executions      []Execution `json:"executions"`
}

func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// CanonicalJSON provides deterministic edition-independent evidence bytes.
// Identity is supplied by trusted orchestration; validation is not authorization.
func (r Record) CanonicalJSON() ([]byte, error) {
	if r.Schema != VerificationSchema || !ValidIdentifier(r.VerifierID) || !ValidText(r.VerifierVersion, 128) || !ValidIdentifier(r.Capability) || r.Subject.Validate() != nil || len(r.Executions) > MaxExecutions {
		return nil, ErrInvalidEvidence
	}
	var err error
	r.Report, err = r.Report.Canonical()
	if err != nil {
		return nil, err
	}
	r.Executions = append([]Execution{}, r.Executions...)
	seen := map[string]bool{}
	for _, e := range r.Executions {
		if !ValidIdentifier(e.StepID) || seen[e.StepID] || e.ExitCode < -1 {
			return nil, ErrInvalidEvidence
		}
		seen[e.StepID] = true
		for _, digest := range []string{e.StdoutSHA256, e.StderrSHA256} {
			bytes, err := hex.DecodeString(digest)
			if err != nil || len(bytes) != sha256.Size || strings.ToLower(digest) != digest {
				return nil, ErrInvalidEvidence
			}
		}
		switch e.Status {
		case "COMPLETED", "FAILED", "TIMED_OUT", "CANCELLED", "OUTPUT_LIMIT", "UNKNOWN":
		default:
			return nil, ErrInvalidEvidence
		}
		if r.Report.Status == StatusPass && (e.Status != "COMPLETED" || e.ExitCode != 0) {
			return nil, ErrInvalidEvidence
		}
	}
	if r.Report.Status == StatusPass && len(r.Executions) == 0 {
		return nil, ErrInvalidEvidence
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxEvidenceBytes {
		return nil, ErrInvalidEvidence
	}
	return data, nil
}
