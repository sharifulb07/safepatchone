// Package composition evaluates composed proof evidence using shared domain types.
package composition

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"safepatch/internal/domain"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/domain/organization"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
)

const ManifestSchema = "safepatch.composition.v1"
const ResultSchema = "safepatch.composition-result.v1"
const MaxComponents = 128
const MaxConnections = 1024
const MaxBytes = 1024 * 1024

var ErrInvalid = errors.New("invalid composition proof")

type Component struct {
	ID             string `json:"id"`
	RepositoryID   string `json:"repository_id"`
	Commit         string `json:"commit"`
	PassportDigest string `json:"passport_digest"`
}
type Connection struct {
	ID             string `json:"id"`
	From           string `json:"from"`
	To             string `json:"to"`
	ContractDigest string `json:"contract_digest"`
}
type Fields struct {
	Schema         string       `json:"schema"`
	ID             string       `json:"id"`
	OrganizationID string       `json:"organization_id"`
	PolicyDigest   string       `json:"policy_digest"`
	Components     []Component  `json:"components"`
	Connections    []Connection `json:"connections"`
}

// Manifest is an immutable, explicitly declared composition. It does not prove
// discovery completeness. IDs/digests never instruct the engine to fetch URLs.
type Manifest struct{ canonical string }

func New(f Fields) (Manifest, error) {
	bad := Manifest{}
	if f.Schema != ManifestSchema || !evidence.ValidIdentifier(f.ID) || !organization.ValidID(f.OrganizationID) || !passport.ValidDigest(f.PolicyDigest) || len(f.Components) < 2 || len(f.Components) > MaxComponents || len(f.Connections) == 0 || len(f.Connections) > MaxConnections {
		return bad, ErrInvalid
	}
	f.Components = slices.Clone(f.Components)
	f.Connections = slices.Clone(f.Connections)
	seen := map[string]bool{}
	for _, c := range f.Components {
		if !evidence.ValidIdentifier(c.ID) || !organization.ValidID(c.RepositoryID) || !domain.ValidCommitSHA(c.Commit) || !passport.ValidDigest(c.PassportDigest) || seen[c.ID] {
			return bad, ErrInvalid
		}
		seen[c.ID] = true
	}
	edges := map[string]bool{}
	pairs := map[string]bool{}
	for _, c := range f.Connections {
		pair := c.From + "\x00" + c.To + "\x00" + c.ContractDigest
		// A connection must never share the combined-proof subject identity.
		if !evidence.ValidIdentifier(c.ID) || c.ID == f.ID || edges[c.ID] || !seen[c.From] || !seen[c.To] || c.From == c.To || !passport.ValidDigest(c.ContractDigest) || pairs[pair] {
			return bad, ErrInvalid
		}
		edges[c.ID] = true
		pairs[pair] = true
	}
	slices.SortFunc(f.Components, func(a, b Component) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(f.Connections, func(a, b Connection) int { return strings.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxBytes {
		return bad, ErrInvalid
	}
	return Manifest{string(raw)}, nil
}
func (m Manifest) Fields() (Fields, error) {
	var f Fields
	if m.canonical == "" || json.Unmarshal([]byte(m.canonical), &f) != nil {
		return f, ErrInvalid
	}
	return f, nil
}
func (m Manifest) MarshalJSON() ([]byte, error) {
	if m.canonical == "" {
		return nil, ErrInvalid
	}
	return []byte(m.canonical), nil
}
func (m Manifest) Digest() string {
	if m.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(m.canonical))
}
func Decode(raw []byte) (Manifest, error) {
	checked, err := jsonobject.Normalize(raw, MaxBytes)
	if err != nil {
		return Manifest{}, ErrInvalid
	}
	var f Fields
	if json.Unmarshal(checked, &f) != nil {
		return Manifest{}, ErrInvalid
	}
	m, err := New(f)
	if err != nil || !sameJSON(checked, []byte(m.canonical)) {
		return Manifest{}, ErrInvalid
	}
	return m, nil
}
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	a, _ = json.Marshal(x)
	b, _ = json.Marshal(y)
	return bytes.Equal(a, b)
}
