// Package upgrade defines edition-neutral, pinned dependency upgrade candidates.
package upgrade

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	"safepatch/internal/domain"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
)

const Schema = "safepatch.safe-upgrade.v1"
const MaxBytes = 256 * 1024

var ErrInvalid = errors.New("invalid dependency upgrade candidate")
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9@][a-zA-Z0-9@._/+-]{0,255}$`)
var versionPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._+-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Dependency struct {
	Ecosystem   string `json:"ecosystem"`
	Name        string `json:"name"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
}

// File pins every changed manifest/lockfile. The trusted source must confirm
// there are no undeclared changes and that versions match these exact contents.
type File struct {
	Path         string `json:"path"`
	BeforeDigest string `json:"before_digest"`
	AfterDigest  string `json:"after_digest"`
}
type Fields struct {
	Schema     string        `json:"schema"`
	Change     domain.Change `json:"change"`
	Dependency Dependency    `json:"dependency"`
	Files      []File        `json:"files"`
}
type Candidate struct{ canonical string }

func New(f Fields) (Candidate, error) {
	bad := Candidate{}
	d := f.Dependency
	if f.Schema != Schema || f.Change.Validate() != nil || f.Change.BaseSHA == f.Change.HeadSHA || !slices.Contains([]string{"go", "npm", "pypi", "maven", "cargo", "nuget"}, d.Ecosystem) || !namePattern.MatchString(d.Name) || strings.Contains(d.Name, "..") || !versionPattern.MatchString(d.FromVersion) || !versionPattern.MatchString(d.ToVersion) || d.FromVersion == d.ToVersion || len(f.Files) == 0 || len(f.Files) > 32 {
		return bad, ErrInvalid
	}
	f.Files = slices.Clone(f.Files)
	seen := map[string]bool{}
	for _, file := range f.Files {
		if !evidence.ValidPath(file.Path) || seen[file.Path] || !digestPattern.MatchString(file.BeforeDigest) || !digestPattern.MatchString(file.AfterDigest) || file.BeforeDigest == file.AfterDigest {
			return bad, ErrInvalid
		}
		seen[file.Path] = true
	}
	slices.SortFunc(f.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return bad, ErrInvalid
	}
	return Candidate{string(raw)}, nil
}
func (c Candidate) Fields() (Fields, error) {
	var f Fields
	if c.canonical == "" || json.Unmarshal([]byte(c.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (c Candidate) MarshalJSON() ([]byte, error) {
	if c.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(c.canonical), nil
}
func (*Candidate) UnmarshalJSON([]byte) error { return ErrInvalid }
func (c Candidate) ID() string {
	if c.canonical == "" {
		return ""
	}
	return "upgrade_" + evidence.Digest([]byte(Schema+"\n"+c.canonical))
}
func ValidID(id string) bool {
	return strings.HasPrefix(id, "upgrade_") && digestPattern.MatchString(strings.TrimPrefix(id, "upgrade_"))
}
func Decode(raw []byte) (Candidate, error) {
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return Candidate{}, ErrInvalid
	}
	var f Fields
	if json.Unmarshal(checked, &f) != nil {
		return Candidate{}, ErrInvalid
	}
	c, err := New(f)
	if err != nil {
		return Candidate{}, err
	}
	encoded, _ := json.Marshal(f)
	var a, b any
	_ = json.Unmarshal(checked, &a)
	_ = json.Unmarshal(encoded, &b)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		return Candidate{}, ErrInvalid
	}
	return c, nil
}
