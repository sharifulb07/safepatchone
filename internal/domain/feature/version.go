package feature

import "strings"

// Versions are bounded SemVer values. Decimal strings avoid numeric overflow
// for large, otherwise valid identifiers. Build metadata has no precedence.
type version struct {
	core []string
	pre  []string
}

func parseVersion(raw string) (version, bool) {
	if raw == "" || len(raw) > 128 {
		return version{}, false
	}
	base, build, hasBuild := strings.Cut(raw, "+")
	if hasBuild && !validIdentifiers(build, false) {
		return version{}, false
	}
	core, pre, hasPre := strings.Cut(base, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for _, p := range parts {
		if !numeric(p) || (len(p) > 1 && p[0] == '0') {
			return version{}, false
		}
	}
	v := version{core: parts}
	if hasPre {
		if !validIdentifiers(pre, true) {
			return version{}, false
		}
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validIdentifiers(s string, prerelease bool) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
		if prerelease && numeric(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareVersion(a, b version) int {
	for i := range a.core {
		if c := compareNumber(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := numeric(x), numeric(y)
		if xn && !yn {
			return -1
		}
		if yn && !xn {
			return 1
		}
		c := strings.Compare(x, y)
		if xn && yn {
			c = compareNumber(x, y)
		}
		if c != 0 {
			return c
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
