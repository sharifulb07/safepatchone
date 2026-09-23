package feature

import (
	"strings"
	"testing"
)

func TestSemanticVersionPrecedence(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0", "999999999999999999999999.0.0"}
	for i, raw := range ordered {
		a, ok := parseVersion(raw)
		if !ok {
			t.Fatalf("rejected valid version %s", raw)
		}
		if compareVersion(a, a) != 0 {
			t.Fatal("version not equal to itself")
		}
		for _, next := range ordered[i+1:] {
			b, _ := parseVersion(next)
			if compareVersion(a, b) >= 0 || compareVersion(b, a) <= 0 {
				t.Fatalf("wrong precedence: %s versus %s", raw, next)
			}
		}
	}
	a, _ := parseVersion("1.0.0+build.001")
	b, _ := parseVersion("1.0.0+different")
	if compareVersion(a, b) != 0 {
		t.Fatal("build metadata affected precedence")
	}
}

func TestMalformedVersions(t *testing.T) {
	for _, raw := range []string{"", "v1.0.0", "1.0", "1.0.0.0", "01.0.0", "1.00.0", "1.0.-1", "1.0.0-", "1.0.0+", "1.0.0-alpha..1", "1.0.0-01", "1.0.0+build+other", "1.0.0-α", "1.0.0-rc_1", " 1.0.0", strings.Repeat("1", 129)} {
		if _, ok := parseVersion(raw); ok {
			t.Errorf("accepted invalid version %q", raw)
		}
	}
}
