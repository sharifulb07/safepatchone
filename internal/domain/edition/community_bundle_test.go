package edition_test

import (
	"testing"

	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
)

func TestCommunityBundleUsesSharedValidation(t *testing.T) {
	if _, err := edition.NewCommunityBundle(nil); err == nil {
		t.Fatal("nil catalog accepted")
	}
	missing, err := feature.NewFeatureCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edition.NewCommunityBundle(missing); err == nil {
		t.Fatal("missing identities accepted")
	}
	for _, version := range []string{"0.1.0", "1.5.2", "2.0.0-rc.1"} {
		fc, err := feature.NewBuiltinCatalog(version, edition.CommunityBundleRegisteredAt())
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := edition.NewCommunityBundle(fc)
		if err != nil {
			t.Fatal(err)
		}
		if bundle.Version.EditionVersionID != "community-2026-v2" || bundle.Version.EditionKey != edition.Community || len(bundle.Features) != 5 {
			t.Fatal("software version changed edition policy", bundle)
		}
	}
}
