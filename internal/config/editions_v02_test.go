package config

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	limits "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

func TestV02MatrixExactNewEntitlementsAndHigherLimits(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	m, err := V02Editions(fc)
	if err != nil {
		t.Fatal(err)
	}
	counts := []int{6, 12, 19, 26, 35}
	for i, entry := range m.Editions {
		if entry.Bundle.Version.EditionKey != edition.Editions()[i].Key || len(entry.Bundle.Features) != counts[i] || len(entry.Limits) != 8 {
			t.Fatal(entry)
		}
		owned := map[feature.Key]bool{}
		for _, mapping := range entry.Bundle.Features {
			def, _ := fc.GetByID(mapping.FeatureID)
			owned[def.Key] = mapping.Enabled
		}
		for _, k := range []feature.Key{feature.KeyEvidenceLocal, feature.KeyPolicyLocalBasic} {
			if !owned[k] {
				t.Fatal("missing shared local grant", k)
			}
		}
		if owned[feature.KeyPolicyLocalAdvanced] != (i >= 1) || owned[feature.KeyProofCloudHistory] != (i >= 1) || owned[feature.KeyPolicyCentral] != (i >= 2) || owned[feature.KeyProofShared] != (i >= 2) || owned[feature.KeyEnterpriseAudit] != (i == 4) || owned[feature.KeyEnterpriseSelfHosted] != (i == 4) {
			t.Fatal("wrong v0.2 capability boundary", entry.Bundle.Version)
		}
		for _, limit := range entry.Limits {
			if limit.Key == limits.LimitReplayRequestsMonthly && limit.State != limits.Disabled {
				t.Fatal("unimplemented replay received quota")
			}
		}
	}
	for i, team := range m.Editions[2].Limits {
		business := m.Editions[3].Limits[i]
		if team.Key != business.Key {
			t.Fatal("limit order drift")
		}
		if team.State == limits.Integer && (business.State != limits.Integer || business.Value <= team.Value) {
			t.Fatal("Business does not have higher limits", team, business)
		}
	}
	for _, mapping := range m.Editions[4].Bundle.Features {
		if mapping.FeatureID == "feat_0026" || mapping.FeatureID == "feat_0027" {
			var meta map[string]string
			_ = json.Unmarshal(mapping.ConfigurationJSON, &meta)
			if meta["scope"] != "foundations" || meta["availability"] != "FUTURE" {
				t.Fatal("full Enterprise feature incorrectly advertised", meta)
			}
		}
	}
}

func TestV02ConfigurationOwnsDataAndProductVersionIsIndependent(t *testing.T) {
	var original []byte
	for _, version := range []string{"0.1.0", "0.2.0", "1.5.2"} {
		fc, _ := feature.NewBuiltinCatalog(version, time.Now())
		m, err := V02Editions(fc)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(m)
		if original == nil {
			original = raw
		}
		if !bytes.Equal(original, raw) {
			t.Fatal("product version changed edition configuration")
		}
		m.Editions[0].Bundle.Features[0].ConfigurationJSON[0] = '!'
		m.Editions[0].Limits[0].Value = 99999
		again, err := V02Editions(fc)
		b, _ := json.Marshal(again)
		if err != nil || !bytes.Equal(original, b) {
			t.Fatal("caller mutated compiled configuration", err)
		}
	}
}

func TestV02ConfigurationFailsClosed(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("0.2.0", time.Now())
	if _, err := V02Editions(nil); err == nil {
		t.Fatal("nil catalog")
	}
	for _, change := range []func(*EditionMatrix){
		func(m *EditionMatrix) { m.Release = "future" }, func(m *EditionMatrix) { m.Editions = m.Editions[:4] },
		func(m *EditionMatrix) { m.Editions[1] = m.Editions[0] }, func(m *EditionMatrix) { m.Editions[0].Limits = nil },
		func(m *EditionMatrix) { m.Editions[0].Limits[0] = m.Editions[0].Limits[1] },
		func(m *EditionMatrix) { m.Editions[0].Limits[0].State = "UNKNOWN" },
		func(m *EditionMatrix) { m.Editions[0].Limits[0].Value = -1 },
		func(m *EditionMatrix) { m.Editions[0].Bundle.Features[0].FeatureID = "unknown" },
		func(m *EditionMatrix) {
			m.Editions[0].Bundle.Features[0].ConfigurationJSON = json.RawMessage(`{"release":"0.2.0","availability":"ENABLED"}`)
		},
	} {
		m, _ := V02Editions(fc)
		change(&m)
		raw, _ := json.Marshal(m)
		if _, err := decodeEditionMatrix(raw, fc); err == nil {
			t.Fatal("invalid matrix accepted")
		}
	}
	if _, err := decodeEditionMatrix([]byte(`{"release":"0.2.0","release":"0.1.0"}`), fc); err == nil {
		t.Fatal("ambiguous JSON accepted")
	}
}
