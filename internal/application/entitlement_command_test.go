package application_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/entitlement"
)

func TestEntitlementShow(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := application.Run("safepatch", []string{"entitlement", "show"}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatal(code, errOut.String())
	}
	for _, text := range []string{"Community", "community-2026-v2", "COMMUNITY_DEFAULT", "Scope: local", "Account/subscription: not required", "Network: not required", "verify.basic", "evidence.local", "intent.basic", "graph.local", "PLANNED", "cloud-only features are unavailable"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing output", text, out.String())
		}
	}
	out.Reset()
	if code := application.Run("safepatch", []string{"entitlement", "show", "--json"}, &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatal(code, errOut.String())
	}
	var report entitlement.CommunityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.BundleVersion != "community-2026-v2" || report.Scope != "local" || report.NetworkRequired || report.AccountRequired {
		t.Fatal(report)
	}
	for _, f := range report.Features {
		if f.Entitled && (f.Key == "proof.cloud_history" || f.Key == "runtime.replay" || f.Key == "enterprise.airgap") {
			t.Fatal("cloud/premium simulation", f)
		}
	}
}

func TestEntitlementUsageAndOutputFailures(t *testing.T) {
	for _, args := range [][]string{{"entitlement", "--help"}, {"entitlement", "help"}, {"entitlement", "show", "--help"}} {
		var out, errOut bytes.Buffer
		if code := application.Run("safepatch", args, &out, &errOut); code != 0 || !strings.Contains(out.String(), "entitlement show") || errOut.Len() != 0 {
			t.Fatal(args, code)
		}
	}
	for _, args := range [][]string{{"entitlement"}, {"entitlement", "set"}, {"entitlement", "show", "--edition=enterprise"}, {"entitlement", "show", "--json", "extra"}, {"entitlement", "show", "untrusted\nPASS"}} {
		var out, errOut bytes.Buffer
		if code := application.Run("safepatch", args, &out, &errOut); code != 2 || out.Len() != 0 || strings.Contains(errOut.String(), "untrusted") {
			t.Fatal(args, code)
		}
	}
	for _, args := range [][]string{{"entitlement", "show"}, {"entitlement", "show", "--json"}, {"entitlement", "--help"}, {"entitlement", "show", "--help"}} {
		var errOut bytes.Buffer
		if code := application.Run("safepatch", args, failingWriter{}, &errOut); code != 1 {
			t.Fatal(args, code)
		}
	}
	var out, errOut bytes.Buffer
	if code := application.Run("safepatch-api", []string{"entitlement", "show"}, &out, &errOut); code != 2 || out.Len() != 0 {
		t.Fatal("local defaults leaked into server", code)
	}
}
