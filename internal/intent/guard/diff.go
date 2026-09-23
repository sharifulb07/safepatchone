// Package guard implements edition-independent deterministic IntentGuard checks.
// Inputs must come from authenticated orchestration; text and hashes are not
// credentials, executable instructions or production authorization.
package guard

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"

	"safepatch/internal/evidence"
)

const (
	MaxDiffBytes    = 2 * 1024 * 1024
	MaxChangedPaths = 512
	MaxPathBytes    = 512
)

var ErrInvalidInput = errors.New("invalid IntentGuard input")

// GitDiff is git diff --raw --no-abbrev -z output (not a patch). Complete must
// be asserted by trusted collection after a successful, unfiltered capture.
// Empty complete output represents no changed tracked paths. Untracked files
// must be included in the trusted snapshot before collecting this manifest.
type GitDiff struct {
	Raw      []byte
	Complete bool
}

// ParseGitDiff reads raw NUL-delimited records without opening repository paths.
// Both sides of renames/copies are checked conservatively. Conflict/unknown
// statuses, abbreviated hashes, combined diffs and malformed records are errors.
func ParseGitDiff(ctx context.Context, raw []byte) ([]string, error) {
	if ctx == nil {
		return nil, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxDiffBytes {
		return nil, ErrInvalidInput
	}
	paths := map[string]bool{}
	read := func() (string, bool) {
		n := bytes.IndexByte(raw, 0)
		if n < 0 {
			return "", false
		}
		value := string(raw[:n])
		raw = raw[n+1:]
		return value, true
	}
	records := 0
	for len(raw) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		records++
		if records > MaxChangedPaths {
			return nil, ErrInvalidInput
		}
		header, ok := read()
		if !ok || !strings.HasPrefix(header, ":") {
			return nil, ErrInvalidInput
		}
		parts := strings.Split(header[1:], " ")
		if len(parts) != 5 || !mode(parts[0]) || !mode(parts[1]) || !objectID(parts[2]) || !objectID(parts[3]) || len(parts[2]) != len(parts[3]) {
			return nil, ErrInvalidInput
		}
		status := parts[4]
		if len(status) == 0 || !strings.ContainsRune("ADMTRC", rune(status[0])) {
			return nil, ErrInvalidInput
		}
		if len(status) > 1 {
			if !strings.ContainsRune("MRC", rune(status[0])) || len(status) > 4 {
				return nil, ErrInvalidInput
			}
			for _, c := range status[1:] {
				if c < '0' || c > '9' {
					return nil, ErrInvalidInput
				}
			}
			score, err := strconv.Atoi(status[1:])
			if err != nil || score > 100 {
				return nil, ErrInvalidInput
			}
		} else if status == "R" || status == "C" {
			return nil, ErrInvalidInput
		}
		// Validate creation/deletion modes, while permitting a zero destination
		// object ID for a working-tree comparison as documented by Git.
		if status == "A" {
			if parts[0] != "000000" || strings.Trim(parts[2], "0") != "" || parts[1] == "000000" {
				return nil, ErrInvalidInput
			}
		} else if status == "D" {
			if parts[1] != "000000" || strings.Trim(parts[3], "0") != "" || parts[0] == "000000" {
				return nil, ErrInvalidInput
			}
		} else if parts[0] == "000000" || parts[1] == "000000" {
			return nil, ErrInvalidInput
		}
		count := 1
		if status[0] == 'R' || status[0] == 'C' {
			count = 2
		}
		for range count {
			p, ok := read()
			if !ok || !safePath(p) {
				return nil, ErrInvalidInput
			}
			paths[p] = true
			if len(paths) > MaxChangedPaths {
				return nil, ErrInvalidInput
			}
		}
	}
	out := make([]string, 0, len(paths))
	for p := range paths {
		out = append(out, p)
	}
	slices.Sort(out)
	return out, nil
}

func safePath(p string) bool {
	return len(p) <= MaxPathBytes && strings.TrimSpace(p) == p && evidence.ValidPath(p)
}
func mode(m string) bool {
	return slices.Contains([]string{"000000", "100644", "100755", "120000", "160000"}, m)
}
func objectID(s string) bool {
	if len(s) != 40 && len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
