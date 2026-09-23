package migrationtest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"safepatch/internal/config"
	"safepatch/internal/domain/edition"
)

func TestV02SQLAndEmbeddedConfigurationMatchAndPreserveHistory(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root(t), "migrations", "000006_v02_edition_matrix.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(data), "$matrix$")
	if len(parts) != 3 {
		t.Fatal("missing canonical matrix")
	}
	var seeded config.EditionMatrix
	if err := json.Unmarshal([]byte(parts[1]), &seeded); err != nil {
		t.Fatal(err)
	}
	c, fc := initialCatalog(t)
	prior := c
	community, err := edition.NewCommunityBundle(fc)
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.Publish(community.Version, community.Features)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := config.V02Editions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range seeded.Editions {
		c, err = c.Publish(entry.Bundle.Version, entry.Bundle.Features)
		if err != nil {
			t.Fatal(err)
		}
		seeded.Editions[i].Bundle, err = c.GetVersion(entry.Bundle.Version.EditionVersionID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Publish(entry.Bundle.Version, entry.Bundle.Features); !errors.Is(err, edition.ErrVersionExists) {
			t.Fatal("replacement allowed", err)
		}
	}
	a, _ := json.Marshal(compiled)
	b, _ := json.Marshal(seeded)
	if string(a) != string(b) {
		t.Fatal("Go/SQL configuration drift")
	}
	for _, original := range initialBundles(t) {
		before, _ := prior.GetVersion(original.Version.EditionVersionID)
		after, _ := c.GetVersion(original.Version.EditionVersionID)
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(after)
		if string(a) != string(b) {
			t.Fatal("historical bundle changed")
		}
	}
	oldCommunity, _ := c.GetVersion(community.Version.EditionVersionID)
	a, _ = json.Marshal(community)
	b, _ = json.Marshal(oldCommunity)
	if string(a) != string(b) {
		t.Fatal("Community v2 changed")
	}
	for _, required := range []string{"CREATE TABLE edition_limits", "BEFORE UPDATE OR DELETE OR TRUNCATE ON edition_limits", "EXECUTE FUNCTION guard_edition_feature_insert()", "REFERENCES quota_limit_keys(key)"} {
		if !strings.Contains(string(data), required) {
			t.Fatal("missing limit history constraint", required)
		}
	}
}

func TestPostgresV02EditionMatrix(t *testing.T) {
	for _, name := range []string{"editions_v02_before.sql", "editions_v02.sql"} {
		if _, err := os.ReadFile(filepath.Join(root(t), "internal", "platform", "migrationtest", name)); err != nil {
			t.Fatal(err)
		}
	}
	testPostgresMigrations(t, []string{"000001_editions.up.sql", "000002_initial_edition_bundles.up.sql", "000003_entitlement_limits_quotas.up.sql", "000004_community_local_bundle.up.sql", "000005_evidence_graph.up.sql", "000006_v02_edition_matrix.up.sql"}, "editions_v02.sql", "23505", "editions_v02_before.sql", "")
}
