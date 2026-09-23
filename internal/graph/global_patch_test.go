package graph

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"safepatch/internal/domain"
)

func globalFixture() GlobalPatchFields {
	a := domain.Change{OrganizationID: "org", RepositoryID: "a", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	b := a
	b.RepositoryID = "b"
	return GlobalPatchFields{Schema: GlobalPatchSchema, OrganizationID: "org", IntentDigest: strings.Repeat("c", 64), Changes: []domain.Change{a, b}, Dependencies: []PatchDependency{{ChangeID: b.ID(), RequiresChangeID: a.ID()}}}
}
func TestGlobalChangeIdentityCanonicalAndImmutable(t *testing.T) {
	f := globalFixture()
	p, err := NewGlobalPatch(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID().Validate() != nil {
		t.Fatal(p.ID())
	}
	raw, _ := p.MarshalJSON()
	fields, _ := p.Fields()
	slices.Reverse(fields.Changes)
	other, err := NewGlobalPatch(fields)
	if err != nil || other.ID() != p.ID() {
		t.Fatal("order-dependent ID", err)
	}
	unordered, _ := json.Marshal(fields)
	decoded, err := DecodeGlobalPatch(unordered)
	if err != nil || decoded.ID() != p.ID() {
		t.Fatal("unordered decode", err)
	}
	f.Changes[0].HeadSHA = strings.Repeat("d", 40)
	f.Dependencies[0].ChangeID = "poison"
	fields.Changes[0].HeadSHA = strings.Repeat("e", 40)
	after, _ := p.MarshalJSON()
	if !bytes.Equal(raw, after) {
		t.Fatal("caller mutated history")
	}
	for _, mode := range []string{"org", "intent", "head", "base", "dependencies", "membership"} {
		t.Run(mode, func(t *testing.T) {
			f := globalFixture()
			f.Dependencies = []PatchDependency{}
			original, _ := NewGlobalPatch(f)
			switch mode {
			case "org":
				f.OrganizationID = "other"
				for i := range f.Changes {
					f.Changes[i].OrganizationID = "other"
				}
			case "intent":
				f.IntentDigest = strings.Repeat("d", 64)
			case "head":
				f.Changes[0].HeadSHA = strings.Repeat("d", 40)
			case "base":
				f.Changes[0].BaseSHA = strings.Repeat("d", 40)
			case "dependencies":
				f.Dependencies = globalFixture().Dependencies
			case "membership":
				c := f.Changes[0]
				c.RepositoryID = "c"
				f.Changes = append(f.Changes, c)
			}
			changed, err := NewGlobalPatch(f)
			if err != nil || changed.ID() == original.ID() {
				t.Fatal("identity not bound", err)
			}
		})
	}
	if _, err := (GlobalPatch{}).MarshalJSON(); err == nil {
		t.Fatal("zero value")
	}
	if (GlobalPatch{}).ID() != "" || GlobalChangeID("globalchange_bad").Validate() == nil {
		t.Fatal("invalid ID")
	}
}
func TestGlobalPatchRejectsAmbiguousOrUnsafeSets(t *testing.T) {
	for _, mode := range []string{"foreign", "duplicate_repo", "one", "many", "noop", "bad_sha", "bad_org", "bad_digest", "nil_deps", "many_deps", "duplicate_edge", "missing", "self", "cycle"} {
		t.Run(mode, func(t *testing.T) {
			f := globalFixture()
			switch mode {
			case "foreign":
				f.Changes[1].OrganizationID = "other"
			case "duplicate_repo":
				f.Changes[1].RepositoryID = "a"
			case "one":
				f.Changes = f.Changes[:1]
			case "many":
				f.Changes = make([]domain.Change, MaxRepositories+1)
			case "noop":
				f.Changes[0].HeadSHA = f.Changes[0].BaseSHA
			case "bad_sha":
				f.Changes[0].HeadSHA = "main"
			case "bad_org":
				f.OrganizationID = "../org"
			case "bad_digest":
				f.IntentDigest = "https://internal/secret"
			case "nil_deps":
				f.Dependencies = nil
			case "many_deps":
				f.Dependencies = make([]PatchDependency, MaxGlobalDependencies+1)
			case "duplicate_edge":
				f.Dependencies = append(f.Dependencies, f.Dependencies[0])
			case "missing":
				f.Dependencies[0].RequiresChangeID = "change_missing"
			case "self":
				f.Dependencies[0].RequiresChangeID = f.Dependencies[0].ChangeID
			case "cycle":
				d := f.Dependencies[0]
				f.Dependencies = append(f.Dependencies, PatchDependency{d.RequiresChangeID, d.ChangeID})
			}
			if _, err := NewGlobalPatch(f); err == nil {
				t.Fatal("accepted", mode)
			}
		})
	}
	p, _ := NewGlobalPatch(globalFixture())
	raw, _ := p.MarshalJSON()
	for _, bad := range [][]byte{[]byte(`null`), append(raw, []byte(`{}`)...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1), bytes.Replace(raw, []byte(`"organization_id":"org"`), []byte(`"organization_id":"org","organization_id":"evil"`), 1), bytes.Replace(raw, []byte(`"schema":`), []byte(`"unknown":1,"schema":`), 1), bytes.Repeat([]byte(" "), MaxGlobalPatchBytes+1)} {
		if _, err := DecodeGlobalPatch(bad); err == nil {
			t.Fatal("unsafe JSON")
		}
	}
}
func FuzzGlobalPatch(f *testing.F) {
	p, _ := NewGlobalPatch(globalFixture())
	raw, _ := p.MarshalJSON()
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		p, err := DecodeGlobalPatch(raw)
		if err == nil {
			encoded, _ := p.MarshalJSON()
			q, err := DecodeGlobalPatch(encoded)
			if err != nil || p.ID() != q.ID() {
				t.Fatal("unstable identity")
			}
		}
	})
}
