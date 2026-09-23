// Package release defines distribution metadata, independent of commercial editions.
package release

import (
	"errors"
	"regexp"
)

type Channel string

const (
	Stable  Channel = "stable"
	Beta    Channel = "beta"
	Nightly Channel = "nightly"
	LTS     Channel = "lts"
)

var ErrChannel = errors.New("unknown release channel")
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Channels returns a fresh collection so callers cannot change platform keys.
func Channels() []Channel { return []Channel{Stable, Beta, Nightly, LTS} }

// ParseChannel accepts only canonical keys and the historical uppercase LTS alias.
// Whitespace, arbitrary case folding and unknown values never imply stable.
func ParseChannel(value string) (Channel, error) {
	if value == "LTS" {
		value = string(LTS)
	}
	c := Channel(value)
	switch c {
	case Stable, Beta, Nightly, LTS:
		return c, nil
	}
	return "", ErrChannel
}

func ValidVersion(value string) bool { return len(value) <= 128 && versionPattern.MatchString(value) }
