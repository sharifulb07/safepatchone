package application

import (
	"bytes"
	"encoding/json"
	"testing"

	"safepatch/internal/domain/edition"
)

func TestBuildIdentityIndependentOfEveryRuntimeEdition(t *testing.T) {
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			t.Setenv("SAFEPATCH_EDITION", string(ed.Key))
			for _, command := range []string{"safepatch", "safepatch-api", "safepatch-worker", "safepatch-collector", "safepatch-mcp-gateway", "safepatch-replay"} {
				var out, errs bytes.Buffer
				if code := Run(command, []string{"version", "--json"}, &out, &errs); code != 0 || errs.Len() != 0 {
					t.Fatal(code, errs.String())
				}
				var got versionInfo
				if json.Unmarshal(out.Bytes(), &got) != nil || got.Version != BuildVersion || got.Commit != BuildCommit || got.BuiltAt != BuildTime {
					t.Fatal(out.String())
				}
				if got.Edition != edition.Community || got.EditionConfiguration != edition.CommunityBundleVersion || got.EntitlementSource != "community_default" || got.Protocol != 1 || got.ReleaseChannel != "stable" {
					t.Fatal("environment spoofed resolved local metadata", got)
				}
			}
		})
	}
}
