package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
)

var ErrIntegrity = errors.New("verification integrity unavailable or blocked")

const MaxControlBytes = 1024 * 1024

type ControlCheck struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	ConfigDigest string `json:"config_digest"`
}
type ControlFields struct {
	Schema               string           `json:"schema"`
	OrganizationID       string           `json:"organization_id"`
	Subject              evidence.Subject `json:"subject"`
	Complete             bool             `json:"complete"`
	Scanners             []ControlCheck   `json:"scanners"`
	Tests                []ControlCheck   `json:"tests"`
	CoverageThresholdBPS *uint32          `json:"coverage_threshold_bps"`
	TimeoutMillis        int64            `json:"timeout_millis"`
	SecurityExclusions   []string         `json:"security_exclusions"`
	Policy               json.RawMessage  `json:"policy"`
}

// VerificationControls owns immutable normalized configuration, never executable
// scripts. Complete must come from a trusted extractor of all control surfaces.
type VerificationControls struct{ canonical string }

func (c VerificationControls) Fields() (ControlFields, error) {
	var f ControlFields
	if c.canonical == "" || json.Unmarshal([]byte(c.canonical), &f) != nil {
		return f, ErrIntegrity
	}
	return f, nil
}
func (c VerificationControls) MarshalJSON() ([]byte, error) {
	if c.canonical == "" {
		return nil, ErrIntegrity
	}
	return []byte(c.canonical), nil
}
func (*VerificationControls) UnmarshalJSON([]byte) error { return ErrIntegrity }
func (c VerificationControls) Digest() string {
	if c.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(c.canonical))
}

// ConfigurationDigest excludes only the pinned revision; tenant and repository
// remain bound. A policy version change still requires independent review.
func (c VerificationControls) ConfigurationDigest() string {
	f, err := c.Fields()
	if err != nil {
		return ""
	}
	f.Subject.Revision = ""
	raw, _ := json.Marshal(f)
	return evidence.Digest(raw)
}
func NewVerificationControls(f ControlFields) (VerificationControls, error) {
	if f.Schema != "safepatch.verification-controls.v1" || !tenant.ValidID(f.OrganizationID) || f.Subject.Validate() != nil || f.Scanners == nil || f.Tests == nil || f.SecurityExclusions == nil || len(f.Scanners) > 256 || len(f.Tests) > 4096 || len(f.SecurityExclusions) > 256 || f.TimeoutMillis < 1 || f.TimeoutMillis > 86_400_000 || f.CoverageThresholdBPS != nil && *f.CoverageThresholdBPS > 10000 {
		return VerificationControls{}, ErrIntegrity
	}
	p, err := policy.Decode(f.Policy)
	if err != nil {
		return VerificationControls{}, ErrIntegrity
	}
	pd, _ := p.Document()
	if pd.TenantID != f.OrganizationID {
		return VerificationControls{}, ErrIntegrity
	}
	f.Policy, _ = json.Marshal(p)
	normalize := func(checks []ControlCheck) ([]ControlCheck, error) {
		checks = append([]ControlCheck{}, checks...)
		sort.Slice(checks, func(i, j int) bool { return checks[i].ID < checks[j].ID })
		for i, c := range checks {
			if !evidence.ValidIdentifier(c.ID) || (i > 0 && c.ID == checks[i-1].ID) || (evidence.OriginalReference{SHA256: c.ConfigDigest}).Validate() != nil {
				return nil, ErrIntegrity
			}
		}
		return checks, nil
	}
	f.Scanners, err = normalize(f.Scanners)
	if err != nil {
		return VerificationControls{}, err
	}
	f.Tests, err = normalize(f.Tests)
	if err != nil {
		return VerificationControls{}, err
	}
	f.SecurityExclusions = append([]string{}, f.SecurityExclusions...)
	slices.Sort(f.SecurityExclusions)
	for i, s := range f.SecurityExclusions {
		if s == "" || !evidence.ValidText(s, 512) || i > 0 && s == f.SecurityExclusions[i-1] {
			return VerificationControls{}, ErrIntegrity
		}
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxControlBytes {
		return VerificationControls{}, ErrIntegrity
	}
	return VerificationControls{string(raw)}, nil
}
func DecodeVerificationControls(raw []byte) (VerificationControls, error) {
	clean, err := jsonobject.Normalize(raw, MaxControlBytes)
	if err != nil {
		return VerificationControls{}, ErrIntegrity
	}
	var f ControlFields
	if json.Unmarshal(clean, &f) != nil {
		return VerificationControls{}, ErrIntegrity
	}
	typed, _ := json.Marshal(f)
	canonical := func(b []byte) []byte {
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		var v any
		if d.Decode(&v) != nil {
			return nil
		}
		r, _ := json.Marshal(v)
		return r
	}
	if !bytes.Equal(canonical(clean), canonical(typed)) {
		return VerificationControls{}, ErrIntegrity
	}
	return NewVerificationControls(f)
}

type IntegrityFinding struct {
	Code      string `json:"code"`
	ControlID string `json:"control_id"`
}
type IntegrityResult struct {
	Schema     string             `json:"schema"`
	BaseDigest string             `json:"base_digest"`
	HeadDigest string             `json:"head_digest"`
	Status     string             `json:"status"` // INTACT BLOCKED UNKNOWN; never PASS
	Changed    bool               `json:"changed"`
	Findings   []IntegrityFinding `json:"findings"`
}

func UnknownIntegrity() IntegrityResult {
	return IntegrityResult{Schema: "safepatch.verification-integrity.v1", Status: "UNKNOWN", Findings: []IntegrityFinding{}}
}

// VerificationIntegrity has no commercial inputs and no disable switch.
type VerificationIntegrity struct{}

func (VerificationIntegrity) Check(ctx context.Context, base, head VerificationControls) (IntegrityResult, error) {
	r := UnknownIntegrity()
	if ctx == nil {
		return r, ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	a, e1 := base.Fields()
	b, e2 := head.Fields()
	if e1 != nil || e2 != nil || a.OrganizationID != b.OrganizationID || a.Subject.RepositoryID != b.Subject.RepositoryID {
		return r, ErrIntegrity
	}
	r.BaseDigest = base.Digest()
	r.HeadDigest = head.Digest()
	r.Changed = base.ConfigurationDigest() != head.ConfigurationDigest()
	if a.Subject.Revision == b.Subject.Revision && r.Changed {
		return r, ErrIntegrity
	}
	incomplete := !a.Complete || !b.Complete
	add := func(code, id string) { r.Findings = append(r.Findings, IntegrityFinding{code, id}) }
	checks := func(old, next []ControlCheck, removed string) {
		index := map[string]ControlCheck{}
		for _, c := range next {
			index[c.ID] = c
		}
		for _, c := range old {
			if !c.Enabled {
				continue
			}
			n, ok := index[c.ID]
			if !ok || !n.Enabled {
				add(removed, c.ID)
			} else if c.ConfigDigest != n.ConfigDigest {
				incomplete = true
				add("CONTROL_CHANGE_UNCLASSIFIED", c.ID)
			}
		}
	}
	checks(a.Scanners, b.Scanners, "SCANNER_REMOVED")
	checks(a.Tests, b.Tests, "TEST_DISABLED")
	if a.CoverageThresholdBPS != nil && (b.CoverageThresholdBPS == nil || *b.CoverageThresholdBPS < *a.CoverageThresholdBPS) {
		add("COVERAGE_THRESHOLD_REDUCED", "")
	}
	if b.TimeoutMillis < a.TimeoutMillis {
		add("VERIFICATION_TIMEOUT_REDUCED", "")
	}
	for _, s := range b.SecurityExclusions {
		if !slices.Contains(a.SecurityExclusions, s) {
			add("SECURITY_EXCLUSION_ADDED", evidence.Digest([]byte(s)))
		}
	}
	ap, _ := policy.Decode(a.Policy)
	bp, _ := policy.Decode(b.Policy)
	findings, err := (policy.PolicyGuard{}).Check(ctx, ap, bp)
	if err != nil {
		return UnknownIntegrity(), err
	}
	for _, f := range findings {
		add("POLICY_WEAKENED", f.RuleID)
	}
	r.Status = "INTACT"
	if incomplete {
		r.Status = "UNKNOWN"
	}
	for _, f := range r.Findings {
		if f.Code != "CONTROL_CHANGE_UNCLASSIFIED" {
			r.Status = "BLOCKED"
		}
	}
	return r, nil
}
