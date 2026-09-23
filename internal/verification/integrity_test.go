package verification_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"safepatch/internal/evidence"
	"safepatch/internal/policy"
	"safepatch/internal/verification"
)

func controls(t *testing.T, revision string, mutate func(*verification.ControlFields)) verification.VerificationControls {
	t.Helper()
	risk := 20
	p, _ := policy.New(policy.Document{Schema: policy.Schema, PolicyID: "p", TenantID: "org", Version: "1", Rules: []policy.Rule{{ID: "r", Require: policy.Requirements{MaxRiskScore: &risk}}}})
	raw, _ := json.Marshal(p)
	threshold := uint32(8000)
	d := verification.ControlFields{Schema: "safepatch.verification-controls.v1", OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: revision}, Complete: true, Scanners: []verification.ControlCheck{{ID: "security.scan", Enabled: true, ConfigDigest: evidence.Digest([]byte("scanner"))}}, Tests: []verification.ControlCheck{{ID: "go.test", Enabled: true, ConfigDigest: evidence.Digest([]byte("tests"))}}, CoverageThresholdBPS: &threshold, TimeoutMillis: 60000, SecurityExclusions: []string{}, Policy: raw}
	if mutate != nil {
		mutate(&d)
	}
	c, err := verification.NewVerificationControls(d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestVerificationIntegrityDetectors(t *testing.T) {
	for _, mode := range []string{"same", "scanner_removed", "test_disabled", "coverage_reduced", "coverage_removed", "policy_weakened", "exclusion_added", "timeout_reduced", "unclassified", "incomplete", "stronger"} {
		t.Run(mode, func(t *testing.T) {
			base := controls(t, "base", nil)
			head := controls(t, "head", func(f *verification.ControlFields) {
				switch mode {
				case "scanner_removed":
					f.Scanners = []verification.ControlCheck{}
				case "test_disabled":
					f.Tests[0].Enabled = false
				case "coverage_reduced":
					*f.CoverageThresholdBPS = 7000
				case "coverage_removed":
					f.CoverageThresholdBPS = nil
				case "policy_weakened":
					f.Policy = bytes.Replace(f.Policy, []byte(`"max_risk_score":20`), []byte(`"max_risk_score":30`), 1)
				case "exclusion_added":
					f.SecurityExclusions = []string{"**/security/**"}
				case "timeout_reduced":
					f.TimeoutMillis = 1000
				case "unclassified":
					f.Scanners[0].ConfigDigest = evidence.Digest([]byte("different"))
				case "incomplete":
					f.Complete = false
				case "stronger":
					*f.CoverageThresholdBPS = 9000
					f.TimeoutMillis = 120000
				}
			})
			r, err := (verification.VerificationIntegrity{}).Check(t.Context(), base, head)
			if err != nil {
				t.Fatal(err)
			}
			want := "BLOCKED"
			if mode == "same" || mode == "stronger" {
				want = "INTACT"
			}
			if mode == "unclassified" || mode == "incomplete" {
				want = "UNKNOWN"
			}
			if r.Status != want {
				t.Fatal(mode, r)
			}
			if mode == "same" && r.Changed {
				t.Fatal("revision change mistaken for controls change")
			}
		})
	}
}
func TestControlsImmutabilityAndPoisoning(t *testing.T) {
	c := controls(t, "base", nil)
	raw, _ := json.Marshal(c)
	round, err := verification.DecodeVerificationControls(raw)
	if err != nil || round.Digest() != c.Digest() {
		t.Fatal(err)
	}
	f, _ := c.Fields()
	f.Scanners[0].Enabled = false
	*f.CoverageThresholdBPS = 0
	after, _ := json.Marshal(c)
	if !bytes.Equal(raw, after) {
		t.Fatal("mutated controls")
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte(`"schema":`), []byte(`"schema":"x","schema":`), 1), bytes.Replace(raw, []byte(`"complete":`), []byte(`"Complete":`), 1), append([]byte(`{"disable_integrity":true,`), raw[1:]...), bytes.Repeat([]byte("x"), verification.MaxControlBytes+1)} {
		if _, err := verification.DecodeVerificationControls(bad); err == nil {
			t.Fatal("poison accepted")
		}
	}
	f, _ = c.Fields()
	f.TimeoutMillis = 0
	if _, err := verification.NewVerificationControls(f); err == nil {
		t.Fatal("zero timeout")
	}
	f, _ = c.Fields()
	f.Scanners = append(f.Scanners, f.Scanners[0])
	if _, err := verification.NewVerificationControls(f); err == nil {
		t.Fatal("duplicate scanner")
	}
}
func FuzzVerificationControls(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"safepatch.verification-controls.v1"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := verification.DecodeVerificationControls(raw)
		if err == nil {
			b, _ := json.Marshal(c)
			round, err := verification.DecodeVerificationControls(b)
			if err != nil || round.Digest() != c.Digest() {
				t.Fatal("unstable controls")
			}
		}
	})
}
