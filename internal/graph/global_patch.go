package graph

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"safepatch/internal/domain"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
)

const GlobalPatchSchema = "safepatch.global-patch.v1"
const MaxGlobalPatchBytes = 1024 * 1024
const MaxGlobalDependencies = 1024

// GlobalChangeID identifies one immutable organization-scoped coordinated set.
// It is neither an authorization token nor a verification result.
type GlobalChangeID string

func (id GlobalChangeID) Validate() error {
	if !strings.HasPrefix(string(id), "globalchange_") || !sha256Hash(strings.TrimPrefix(string(id), "globalchange_")) {
		return ErrInvalidGraph
	}
	return nil
}

// PatchDependency means ChangeID requires RequiresChangeID. It declares ordering,
// not evidence that the changes are compatible or safe to deploy together.
type PatchDependency struct {
	ChangeID         string `json:"change_id"`
	RequiresChangeID string `json:"requires_change_id"`
}
type GlobalPatchFields struct {
	Schema         string            `json:"schema"`
	OrganizationID string            `json:"organization_id"`
	IntentDigest   string            `json:"intent_digest"`
	Changes        []domain.Change   `json:"changes"`
	Dependencies   []PatchDependency `json:"dependencies"`
}
type GlobalPatch struct{ canonical string }

func NewGlobalPatch(f GlobalPatchFields) (GlobalPatch, error) {
	bad := GlobalPatch{}
	if f.Schema != GlobalPatchSchema || !tenant.ValidID(f.OrganizationID) || !sha256Hash(f.IntentDigest) || len(f.Changes) < 2 || len(f.Changes) > MaxRepositories || f.Dependencies == nil || len(f.Dependencies) > MaxGlobalDependencies {
		return bad, ErrInvalidGraph
	}
	f.Changes = slices.Clone(f.Changes)
	f.Dependencies = append([]PatchDependency{}, f.Dependencies...)
	repos := map[string]bool{}
	degrees := map[string]int{}
	next := map[string][]string{}
	for _, c := range f.Changes {
		if c.Validate() != nil || c.OrganizationID != f.OrganizationID || c.BaseSHA == c.HeadSHA || repos[c.RepositoryID] {
			return bad, ErrInvalidGraph
		}
		repos[c.RepositoryID] = true
		degrees[c.ID()] = 0
	}
	seen := map[PatchDependency]bool{}
	for _, d := range f.Dependencies {
		_, a := degrees[d.ChangeID]
		_, b := degrees[d.RequiresChangeID]
		if !a || !b || d.ChangeID == d.RequiresChangeID || seen[d] {
			return bad, ErrInvalidGraph
		}
		seen[d] = true
		degrees[d.ChangeID]++
		next[d.RequiresChangeID] = append(next[d.RequiresChangeID], d.ChangeID)
	}
	ready := []string{}
	for id, degree := range degrees {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	visited := 0
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		visited++
		for _, dependent := range next[id] {
			degrees[dependent]--
			if degrees[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if visited != len(f.Changes) {
		return bad, ErrInvalidGraph
	}
	slices.SortFunc(f.Changes, func(a, b domain.Change) int { return strings.Compare(a.RepositoryID, b.RepositoryID) })
	slices.SortFunc(f.Dependencies, func(a, b PatchDependency) int {
		return strings.Compare(a.ChangeID+":"+a.RequiresChangeID, b.ChangeID+":"+b.RequiresChangeID)
	})
	raw, err := json.Marshal(f)
	if err != nil || len(raw) > MaxGlobalPatchBytes {
		return bad, ErrInvalidGraph
	}
	return GlobalPatch{string(raw)}, nil
}
func (p GlobalPatch) Fields() (GlobalPatchFields, error) {
	var f GlobalPatchFields
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &f) != nil {
		return f, ErrInvalidGraph
	}
	return f, nil
}
func (p GlobalPatch) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalidGraph
	}
	return []byte(p.canonical), nil
}
func (*GlobalPatch) UnmarshalJSON([]byte) error { return ErrInvalidGraph }
func (p GlobalPatch) ID() GlobalChangeID {
	if p.canonical == "" {
		return ""
	}
	return GlobalChangeID("globalchange_" + evidence.Digest([]byte(GlobalPatchSchema+"\n"+p.canonical)))
}
func DecodeGlobalPatch(raw []byte) (GlobalPatch, error) {
	normalized, err := jsonobject.Normalize(raw, MaxGlobalPatchBytes)
	if err != nil {
		return GlobalPatch{}, ErrInvalidGraph
	}
	var f GlobalPatchFields
	if json.Unmarshal(normalized, &f) != nil {
		return GlobalPatch{}, ErrInvalidGraph
	}
	p, err := NewGlobalPatch(f)
	if err != nil {
		return GlobalPatch{}, err
	}
	// Compare object shapes before array sorting: reject unknown/case-aliased keys,
	// nulls and omitted fields without requiring caller array order to be canonical.
	original, _ := json.Marshal(f)
	var a, b any
	_ = json.Unmarshal(normalized, &a)
	_ = json.Unmarshal(original, &b)
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	if !bytes.Equal(x, y) {
		return GlobalPatch{}, ErrInvalidGraph
	}
	return p, nil
}
