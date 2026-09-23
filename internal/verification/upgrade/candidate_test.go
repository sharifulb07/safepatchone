package upgrade

import (
	"bytes"
	"encoding/json"
	"safepatch/internal/domain"
	"strings"
	"testing"
)

func fixture() Fields {
	return Fields{Schema: Schema, Change: domain.Change{OrganizationID: "org", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}, Dependency: Dependency{Ecosystem: "go", Name: "example.test/lib", FromVersion: "v1.0.0", ToVersion: "v1.0.1"}, Files: []File{{Path: "go.mod", BeforeDigest: strings.Repeat("a", 64), AfterDigest: strings.Repeat("b", 64)}}}
}
func TestCandidateIdentityAndStrictDecoding(t *testing.T) {
	f := fixture()
	c, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := c.MarshalJSON()
	f.Files[0].Path = "bad"
	after, _ := c.MarshalJSON()
	if !bytes.Equal(raw, after) || !ValidID(c.ID()) {
		t.Fatal("mutable identity")
	}
	for _, mode := range []string{"version", "org", "head", "digest"} {
		f := fixture()
		switch mode {
		case "version":
			f.Dependency.ToVersion = "v2.0.0"
		case "org":
			f.Change.OrganizationID = "other"
		case "head":
			f.Change.HeadSHA = strings.Repeat("c", 40)
		case "digest":
			f.Files[0].AfterDigest = strings.Repeat("c", 64)
		}
		q, err := New(f)
		if err != nil || q.ID() == c.ID() {
			t.Fatal(mode, err)
		}
	}
	decoded, err := Decode(raw)
	if err != nil || decoded.ID() != c.ID() {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`null`), append(raw, []byte(`{}`)...), bytes.Replace(raw, []byte(`"schema":`), []byte(`"Schema":`), 1), bytes.Replace(raw, []byte(`"schema":`), []byte(`"extra":true,"schema":`), 1), bytes.Replace(raw, []byte(`"ecosystem":"go"`), []byte(`"ecosystem":"go","ecosystem":"npm"`), 1), bytes.Repeat([]byte(" "), MaxBytes+1)} {
		if _, err := Decode(bad); err == nil {
			t.Fatal("unsafe input")
		}
	}
}
func TestCandidateRejectsUnsafeAndAmbiguousInputs(t *testing.T) {
	for _, mode := range []string{"command", "version_range", "same_version", "noop", "digest", "path", "duplicate", "many", "empty", "ecosystem"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture()
			switch mode {
			case "command":
				f.Dependency.Name = "x; curl secret"
			case "version_range":
				f.Dependency.ToVersion = "^1.0"
			case "same_version":
				f.Dependency.ToVersion = f.Dependency.FromVersion
			case "noop":
				f.Change.HeadSHA = f.Change.BaseSHA
			case "digest":
				f.Files[0].AfterDigest = f.Files[0].BeforeDigest
			case "path":
				f.Files[0].Path = "../go.mod"
			case "duplicate":
				f.Files = append(f.Files, f.Files[0])
			case "many":
				f.Files = make([]File, 33)
			case "empty":
				f.Files = nil
			case "ecosystem":
				f.Dependency.Ecosystem = "shell"
			}
			if _, err := New(f); err == nil {
				t.Fatal(mode)
			}
		})
	}
}
func FuzzUpgradeCandidate(f *testing.F) {
	c, _ := New(fixture())
	raw, _ := c.MarshalJSON()
	f.Add(raw)
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := Decode(raw)
		if err == nil {
			b, _ := json.Marshal(c)
			d, err := Decode(b)
			if err != nil || d.ID() != c.ID() {
				t.Fatal("unstable identity")
			}
		}
	})
}
