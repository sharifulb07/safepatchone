package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const MaxRecords = 64
const MaxPayloadBytes = 16 * 1024
const MaxDatasetBytes = 4 * 1024 * 1024

var ErrInvalid = errors.New("invalid replay data or configuration")
var ErrUnavailable = errors.New("replay unavailable")
var ErrDenied = errors.New("replay denied")
var ErrUnsafe = errors.New("replay sandbox termination unconfirmed")

func canonical(raw []byte, bound int) ([]byte, error) {
	raw, err := jsonobject.Normalize(raw, bound)
	if err != nil {
		return nil, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(v)
}

type Scope struct {
	OrganizationID string `json:"organization_id"`
	RepositoryID   string `json:"repository_id"`
}

func (s Scope) Tenant() tenant.Scope {
	return tenant.Scope{OrganizationID: s.OrganizationID, ResourceID: s.RepositoryID}
}
func (s Scope) Validate() error { return s.Tenant().Validate() }

// Redactor uses explicit scalar JSON pointers from trusted privacy policy.
// Unlisted fields (including their names) never enter a dataset. Containers
// cannot be allowlisted wholesale. Sensitive names and recognizable secrets
// remain redacted even if accidentally allowlisted. No raw-value hash is kept.
type Redactor struct {
	paths []string
	hash  string
}

var sensitiveValue = regexp.MustCompile(`(?i)(bearer\s|basic\s|[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}|-----BEGIN|(?:sk|ghp|gho|xoxb)[_\-])`)

func NewRedactor(paths []string) (*Redactor, error) {
	if len(paths) == 0 || len(paths) > 64 {
		return nil, ErrInvalid
	}
	paths = slices.Clone(paths)
	slices.Sort(paths)
	for i, p := range paths {
		if len(p) > 256 || !strings.HasPrefix(p, "/") || i > 0 && p == paths[i-1] {
			return nil, ErrInvalid
		}
		parts := strings.Split(p[1:], "/")
		if len(parts) > 8 {
			return nil, ErrInvalid
		}
		for _, part := range parts {
			if !evidence.ValidIdentifier(part) {
				return nil, ErrInvalid
			}
		}
	}
	raw, _ := json.Marshal(paths)
	return &Redactor{paths: paths, hash: evidence.Digest(append([]byte("safepatch.replay.redaction.v1\n"), raw...))}, nil
}
func (r *Redactor) Digest() string {
	if r == nil {
		return ""
	}
	return r.hash
}
func (r *Redactor) Redact(raw []byte) (json.RawMessage, error) {
	if r == nil {
		return nil, ErrInvalid
	}
	raw, err := canonical(raw, MaxPayloadBytes)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var source map[string]any
	if d.Decode(&source) != nil {
		return nil, ErrInvalid
	}
	out := map[string]any{}
	for _, pointer := range r.paths {
		parts := strings.Split(pointer[1:], "/")
		var value any = source
		found := true
		secret := false
		for _, part := range parts {
			lower := strings.ToLower(part)
			for _, name := range []string{"password", "secret", "token", "authorization", "cookie", "email", "phone", "session", "credential"} {
				if strings.Contains(lower, name) {
					secret = true
				}
			}
			object, ok := value.(map[string]any)
			if !ok {
				found = false
				break
			}
			value, found = object[part]
			if !found {
				break
			}
		}
		if !found {
			continue
		}
		switch v := value.(type) {
		case map[string]any, []any:
			return nil, ErrInvalid
		case string:
			if len(v) > 1024 || sensitiveValue.MatchString(v) {
				secret = true
			}
		}
		if secret {
			value = "[REDACTED]"
		}
		dest := out
		for _, part := range parts[:len(parts)-1] {
			if next, ok := dest[part]; ok {
				child, ok := next.(map[string]any)
				if !ok {
					return nil, ErrInvalid
				}
				dest = child
			} else {
				child := map[string]any{}
				dest[part] = child
				dest = child
			}
		}
		dest[parts[len(parts)-1]] = value
	}
	return json.Marshal(out)
}

type Effect string

const Pure Effect = "PURE"
const Read Effect = "READ"
const Write Effect = "WRITE"
const Destructive Effect = "DESTRUCTIVE"
const UnknownEffect Effect = "UNKNOWN"

type Fixture struct {
	ID     string          `json:"id"`
	Effect Effect          `json:"effect"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
}
type Observation struct {
	Complete bool            `json:"complete"`
	Status   int             `json:"status"`
	Output   json.RawMessage `json:"output"`
	Effects  []string        `json:"effects"`
}
type TraceLink struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
}

func validHex(s string, n int) bool {
	if len(s) != n || s == strings.Repeat("0", n) {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (t TraceLink) Validate() error {
	if t.TraceID == "" && t.SpanID == "" {
		return nil
	}
	if !validHex(t.TraceID, 32) || !validHex(t.SpanID, 16) {
		return ErrInvalid
	}
	return nil
}

type Record struct {
	ID          string          `json:"id"`
	Input       json.RawMessage `json:"input"`
	Expected    Observation     `json:"expected"`
	Fixtures    []Fixture       `json:"fixtures"`
	SourceTrace TraceLink       `json:"source_trace"`
}
type DatasetFields struct {
	Schema          string    `json:"schema"`
	Scope           Scope     `json:"scope"`
	ID              string    `json:"dataset_id"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	RedactionDigest string    `json:"redaction_digest"`
	Records         []Record  `json:"records"`
}
type ReplayDataset struct{ canonical string }

func NewDataset(scope Scope, id string, created, expires time.Time, records []Record, redactor *Redactor) (ReplayDataset, error) {
	created = created.UTC().Truncate(time.Microsecond)
	expires = expires.UTC().Truncate(time.Microsecond)
	if scope.Validate() != nil || !tenant.ValidID(id) || created.IsZero() || created.UTC().Year() < 1 || expires.UTC().Year() > 9999 || !expires.After(created) || expires.Sub(created) > 366*24*time.Hour || len(records) == 0 || len(records) > MaxRecords || redactor.Digest() == "" {
		return ReplayDataset{}, ErrInvalid
	}
	f := DatasetFields{Schema: "safepatch.replay.dataset.v1", Scope: scope, ID: id, CreatedAt: created.UTC().Truncate(time.Microsecond), ExpiresAt: expires.UTC().Truncate(time.Microsecond), RedactionDigest: redactor.Digest(), Records: []Record{}}
	seen := map[string]bool{}
	for _, record := range records {
		r := record
		if !tenant.ValidID(r.ID) || seen[r.ID] || r.SourceTrace.Validate() != nil || len(r.Fixtures) > 32 {
			return ReplayDataset{}, ErrInvalid
		}
		seen[r.ID] = true
		var err error
		r.Input, err = redactor.Redact(r.Input)
		if err != nil {
			return ReplayDataset{}, err
		}
		r.Expected, err = redactObservation(r.Expected, redactor)
		if err != nil {
			return ReplayDataset{}, err
		}
		r.Fixtures = append([]Fixture{}, r.Fixtures...)
		ids := map[string]bool{}
		for i, fixture := range r.Fixtures {
			if !tenant.ValidID(fixture.ID) || ids[fixture.ID] || (fixture.Effect != Pure && fixture.Effect != Read && fixture.Effect != Write && fixture.Effect != Destructive && fixture.Effect != UnknownEffect) {
				return ReplayDataset{}, ErrInvalid
			}
			ids[fixture.ID] = true
			fixture.Input, err = redactor.Redact(fixture.Input)
			if err != nil {
				return ReplayDataset{}, err
			}
			fixture.Output, err = redactor.Redact(fixture.Output)
			if err != nil {
				return ReplayDataset{}, err
			}
			r.Fixtures[i] = fixture
		}
		f.Records = append(f.Records, r)
	}
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxDatasetBytes {
		return ReplayDataset{}, ErrInvalid
	}
	return ReplayDataset{string(raw)}, nil
}
func redactObservation(o Observation, r *Redactor) (Observation, error) {
	if o.Status < 0 || o.Status > 599 || o.Complete && o.Status == 0 || len(o.Effects) > 32 {
		return Observation{}, ErrInvalid
	}
	o.Effects = append([]string{}, o.Effects...)
	for _, id := range o.Effects {
		if !tenant.ValidID(id) {
			return Observation{}, ErrInvalid
		}
	}
	var err error
	o.Output, err = r.Redact(o.Output)
	return o, err
}
func (d ReplayDataset) Fields() (DatasetFields, error) {
	var f DatasetFields
	if d.canonical == "" || json.Unmarshal([]byte(d.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (d ReplayDataset) MarshalJSON() ([]byte, error) {
	if d.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(d.canonical), nil
}
func (*ReplayDataset) UnmarshalJSON([]byte) error { return ErrInvalid }
func (d ReplayDataset) Digest() string {
	if d.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(d.canonical))
}

// Decode requires the same trusted redaction policy and re-applies it. Privacy
// policy drift never silently broadens a stored dataset's available information.
func DecodeDataset(raw []byte, r *Redactor) (ReplayDataset, error) {
	checked, err := canonical(raw, MaxDatasetBytes)
	if err != nil {
		return ReplayDataset{}, err
	}
	var f DatasetFields
	if json.Unmarshal(checked, &f) != nil || f.RedactionDigest != r.Digest() {
		return ReplayDataset{}, ErrInvalid
	}
	d, err := NewDataset(f.Scope, f.ID, f.CreatedAt, f.ExpiresAt, f.Records, r)
	if err != nil {
		return ReplayDataset{}, err
	}
	want, _ := canonical([]byte(d.canonical), MaxDatasetBytes)
	if !bytes.Equal(want, checked) {
		return ReplayDataset{}, ErrInvalid
	}
	return d, nil
}
