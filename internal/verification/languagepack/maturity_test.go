package languagepack_test

import (
	"safepatch/internal/verification/languagepack"
	"testing"
)

type maturityClaim struct {
	languagepack.LanguagePack
	level languagepack.Maturity
}

func (p maturityClaim) Descriptor() languagepack.Descriptor {
	d := p.LanguagePack.Descriptor()
	d.Maturity = p.level
	return d
}
func TestMaturityVocabularyAndInflatedClaims(t *testing.T) {
	levels := languagepack.Maturities()
	rubric := languagepack.MaturityRubric()
	if len(levels) != 9 || len(rubric) != 9 {
		t.Fatal("PV vocabulary")
	}
	for i, l := range levels {
		if !l.Valid() || l.Rank() != i || rubric[i].Level != l || rubric[i].Requirement == "" {
			t.Fatal(l)
		}
	}
	levels[0] = "invalid"
	rubric[0].Requirement = "poison"
	if languagepack.Maturities()[0] != languagepack.PV0 || languagepack.MaturityRubric()[0].Requirement == "poison" {
		t.Fatal("mutable rubric")
	}
	for _, l := range []languagepack.Maturity{"", "pv3", "PV9", "Enterprise"} {
		if l.Valid() || l.Rank() != -1 {
			t.Fatal(l)
		}
	}
	p, err := languagepack.New(definition())
	if err != nil {
		t.Fatal(err)
	}
	if p.Descriptor().Maturity != languagepack.PV2 {
		t.Fatal("normalization inflated")
	}
	for _, l := range []languagepack.Maturity{languagepack.PV0, languagepack.PV1, languagepack.PV3, languagepack.PV4, languagepack.PV5, languagepack.PV6, languagepack.PV7, languagepack.PV8, "invalid"} {
		if _, err := languagepack.NewRegistry(maturityClaim{p, l}); err == nil {
			t.Fatal("unsupported maturity accepted", l)
		}
	}
	if _, err := languagepack.NewRegistry(maturityClaim{p, languagepack.PV2}); err != nil {
		t.Fatal(err)
	}
}
