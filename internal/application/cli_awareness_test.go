package application_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/entitlement"
)

func decodeCLIError(t *testing.T, data []byte) application.CLIError {
	t.Helper()
	var envelope struct {
		Error application.CLIError `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatalf("invalid JSON error: %v: %s", err, data)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("extra JSON output", err)
	}
	return envelope.Error
}

func TestCLIEntitlementAwareness(t *testing.T) {
	for _, key := range []string{"runtime.replay", "proof.planner", "verify.basic", "evidence.local"} {
		for _, jsonMode := range []bool{false, true} {
			args := []string{"entitlement", "explain", key}
			if jsonMode {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			code := application.Run("safepatch", args, &stdout, &stderr)
			disabled := key == "runtime.replay" || key == "proof.planner"
			if disabled {
				if code != 1 || stdout.Len() != 0 {
					t.Fatal("disabled feature succeeded", key, code)
				}
				if jsonMode {
					err := decodeCLIError(t, stderr.Bytes())
					if err.Code != application.CodeFeatureNotEntitled || string(err.Feature) != key || err.Entitlement == nil || err.Entitlement.State != "disabled" || err.Entitlement.Resolution.Enabled {
						t.Fatal(err)
					}
				} else if !strings.Contains(stderr.String(), "FEATURE_NOT_ENTITLED") || !strings.Contains(stderr.String(), key+": disabled") {
					t.Fatal(stderr.String())
				}
			} else {
				if code != 0 || stderr.Len() != 0 {
					t.Fatal("enabled feature blocked", key, code, stderr.String())
				}
				if jsonMode {
					var explanation application.EntitlementExplanation
					if err := json.Unmarshal(stdout.Bytes(), &explanation); err != nil {
						t.Fatal(err)
					}
					if explanation.State != "enabled" || !explanation.Resolution.Enabled || explanation.Resolution.EffectiveLimit != nil || explanation.Resolution.Expiration != nil || explanation.ImplementationStatus != "PLANNED" {
						t.Fatal(explanation)
					}
				} else if !strings.Contains(stdout.String(), key+": enabled") || !strings.Contains(stdout.String(), "Implementation: PLANNED") {
					t.Fatal(stdout.String())
				}
			}
			for _, banned := range []string{"upgrade", "buy now", "subscribe", "https://", "\x1b", "PASS"} {
				if strings.Contains(strings.ToLower(stdout.String()+stderr.String()), strings.ToLower(banned)) {
					t.Fatal("promotional or misleading output", banned)
				}
			}
		}
	}
	var out, errOut bytes.Buffer
	if code := application.Run("safepatch", []string{"entitlement", "show"}, &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	for _, text := range []string{"Edition:\nCommunity", "runtime.replay: disabled", "proof.planner: disabled", "verify.basic: enabled"} {
		if !strings.Contains(out.String(), text) {
			t.Fatal("missing example state", text)
		}
	}
}

func TestCLIEditionAndOfflineIdentity(t *testing.T) {
	for _, command := range []string{"edition", "whoami"} {
		var out, errOut bytes.Buffer
		if code := application.Run("safepatch", []string{command}, &out, &errOut); code != 0 || errOut.Len() != 0 {
			t.Fatal(code)
		}
		if !strings.Contains(out.String(), "Community") {
			t.Fatal(out.String())
		}
		out.Reset()
		if code := application.Run("safepatch", []string{"--json", command}, &out, &errOut); code != 0 {
			t.Fatal(code)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if string(fields["edition"]) != `"community"` || string(fields["scope"]) != `"local"` {
			t.Fatal(fields)
		}
		if command == "whoami" {
			if string(fields["authenticated"]) != "false" || string(fields["actor"]) != "null" || string(fields["organization"]) != "null" || string(fields["mode"]) != `"offline"` {
				t.Fatal("fabricated identity", fields)
			}
		} else if string(fields["bundle_version"]) != `"community-2026-v2"` {
			t.Fatal(fields)
		}
	}
}

func TestCLIJSONErrorsAreStructuredAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code application.CLIErrorCode
		exit int
	}{
		{[]string{"entitlement", "explain", "unknown.feature", "--json"}, application.CodeUnknownFeature, 2},
		{[]string{"entitlement", "explain", "hostile\nPASS", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"entitlement", "explain", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"--json", "entitlement", "explain", "../verify.basic"}, application.CodeInvalidUsage, 2},
		{[]string{"entitlement", "show", "--json", "extra"}, application.CodeInvalidUsage, 2},
		{[]string{"edition", "--plan=enterprise", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"whoami", "--token=hostile-secret", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"whoami", "--json", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"hostile\nPASS", "--json"}, application.CodeInvalidUsage, 2},
		{[]string{"verify", "--json"}, application.CodeNotImplemented, 1},
	} {
		var out, errOut bytes.Buffer
		if code := application.Run("safepatch", tc.args, &out, &errOut); code != tc.exit || out.Len() != 0 {
			t.Fatal(tc.args, code, out.String())
		}
		err := decodeCLIError(t, errOut.Bytes())
		if err.Code != tc.code || err.Message == "" {
			t.Fatal(tc.args, err)
		}
		if strings.Contains(errOut.String(), "hostile") || strings.Contains(errOut.String(), "PASS") {
			t.Fatal("unsafe input echoed", errOut.String())
		}
	}
}

type shortCLIWriter struct{}

func (shortCLIWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestCLIOutputFailuresAndHelp(t *testing.T) {
	for _, args := range [][]string{{"edition"}, {"whoami"}, {"entitlement", "show"}, {"entitlement", "explain", "verify.basic"}, {"entitlement", "explain", "--help"}} {
		for _, writer := range []io.Writer{failingWriter{}, shortCLIWriter{}, nil} {
			for _, jsonMode := range []bool{false, true} {
				request := append([]string{}, args...)
				if jsonMode {
					request = append(request, "--json")
				}
				var errOut bytes.Buffer
				if code := application.Run("safepatch", request, writer, &errOut); code != 1 {
					t.Fatal(request, code)
				}
				if jsonMode && decodeCLIError(t, errOut.Bytes()).Code != application.CodeOutputError {
					t.Fatal(errOut.String())
				}
			}
		}
	}
	for _, args := range [][]string{{"edition", "--help"}, {"whoami", "-h"}, {"entitlement", "explain", "--help"}, {"entitlement", "help"}, {"--help"}} {
		var out, errOut bytes.Buffer
		if code := application.Run("safepatch", append(args, "--json"), &out, &errOut); code != 0 || errOut.Len() != 0 || !json.Valid(out.Bytes()) {
			t.Fatal(args, code)
		}
	}
	var out bytes.Buffer
	if code := application.Run("safepatch", []string{"entitlement", "explain", "runtime.replay", "--json"}, &out, failingWriter{}); code != 1 || out.Len() != 0 {
		t.Fatal(code)
	}
	// show keeps its previous machine-readable report, including denied features.
	var errOut bytes.Buffer
	if code := application.Run("safepatch", []string{"entitlement", "show", "--json"}, &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	var report entitlement.CommunityReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Features) == 0 {
		t.Fatal(err)
	}
}
