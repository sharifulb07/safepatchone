package release_test

import (
	"safepatch/internal/domain/release"
	"testing"
)

func TestFourChannelsAndLegacyAlias(t *testing.T) {
	channels := release.Channels()
	if len(channels) != 4 {
		t.Fatal(channels)
	}
	for _, c := range channels {
		if got, err := release.ParseChannel(string(c)); err != nil || got != c {
			t.Fatal(got, err)
		}
	}
	channels[0] = "poison"
	if release.Channels()[0] != release.Stable {
		t.Fatal("mutable channel catalog")
	}
	if c, err := release.ParseChannel("LTS"); err != nil || c != release.LTS {
		t.Fatal(c, err)
	}
	for _, s := range []string{"", "enterprise", "Beta", "STABLE", " lts", "lts ", "lts/../stable", "lts\n", "preview"} {
		if _, err := release.ParseChannel(s); err == nil {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"1.5.2", "0.0.0", "12.30.400"} {
		if !release.ValidVersion(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"v1.5.2", "1.5.2-beta", "enterprise", "01.2.3"} {
		if release.ValidVersion(s) {
			t.Fatal(s)
		}
	}
}
