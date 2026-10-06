package downloads_test

import (
	"encoding/json"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/domain/feature"
)

func TestOrganizationCatalogRechecksAccessAndNeverSigns(t *testing.T) {
	a, signer, _, mux := fixture(t, "org")
	path := "/v1/organizations/org/downloads"
	w := request(mux, "GET", path, "valid")
	var catalog struct {
		Items []application.PublicDownload `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Items) == 0 || signer.calls != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, item := range catalog.Items {
		if strings.Contains(item.Filename, "cli.zip") {
			t.Fatal("public CLI duplicated in private catalog")
		}
	}
	a.grants[feature.KeyEnterpriseSelfHosted] = false
	w = request(mux, "GET", path, "valid")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &catalog) != nil {
		t.Fatal(w.Code)
	}
	for _, item := range catalog.Items {
		if item.ID == "host" || item.ID == "airgap" || item.ID == "host_lts" {
			t.Fatal("revoked capability exposed", item.ID)
		}
	}
	a.fail = true
	if w = request(mux, "GET", path, "valid"); w.Code != 503 || strings.Contains(w.Body.String(), "private billing") {
		t.Fatal("outage presented as empty success", w.Code)
	}
	a.fail = false
	a.allowed = false
	if w = request(mux, "GET", path, "valid"); w.Code != 403 {
		t.Fatal("permission bypass", w.Code)
	}
	a.allowed = true
	if w = request(mux, "GET", "/v1/organizations/victim/downloads", "valid"); w.Code != 403 {
		t.Fatal("tenant bypass", w.Code)
	}
	if w = request(mux, "GET", path, "invalid"); w.Code != 401 {
		t.Fatal("authentication bypass", w.Code)
	}
	if signer.calls != 0 {
		t.Fatal("catalog signed private objects")
	}
}
