package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"safepatch/internal/entitlement"
)

// Exercise the actual composition roots, including process exit codes. The
// child builds share one module and need no infrastructure or network service.
func TestCommandEntrypoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	outputDir := t.TempDir()
	build := exec.CommandContext(ctx, "go", "build", "-o", outputDir+string(os.PathSeparator), "./cmd/...")
	build.Dir = repositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build command entrypoints: %v\n%s", err, output)
	}
	// The provider's dependency closure must remain offline. This does not
	// prohibit future online commands in the shared CLI. Core import policy also
	// rejects hidden infrastructure under tagged/generated/test files.
	deps := exec.CommandContext(ctx, "go", "list", "-deps", "./internal/entitlement")
	deps.Dir = repositoryRoot(t)
	dependencyOutput, err := deps.Output()
	if err != nil {
		t.Fatal("inspect offline provider dependencies", err)
	}
	for _, dependency := range strings.Fields(string(dependencyOutput)) {
		if dependency == "net" || strings.HasPrefix(dependency, "net/") || strings.HasPrefix(dependency, "database/") || dependency == "safepatch/internal/billing" || dependency == "safepatch/internal/licensing" {
			t.Fatal("offline provider acquired an online/account dependency", dependency)
		}
	}
	for _, command := range []string{
		"safepatch", "safepatch-api", "safepatch-worker",
		"safepatch-collector", "safepatch-mcp-gateway", "safepatch-replay",
	} {
		t.Run(command, func(t *testing.T) {
			name := command
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			binary := filepath.Join(outputDir, name)
			output, err := exec.CommandContext(ctx, binary, "--help").CombinedOutput()
			if err != nil || !strings.Contains(string(output), "Usage: "+command) {
				t.Fatalf("help: %v\n%s", err, output)
			}
			output, err = exec.CommandContext(ctx, binary).CombinedOutput()
			assertUnavailable(t, output, err)
			if command == "safepatch" {
				assertOfflineEntitlement(t, ctx, binary)
				output, err = exec.CommandContext(ctx, binary, "verify").CombinedOutput()
				assertUnavailable(t, output, err)
			}
		})
	}
}

func assertOfflineEntitlement(t *testing.T, ctx context.Context, binary string) {
	t.Helper()
	workspace := t.TempDir()
	// Hostile repository configuration is never interpreted as entitlement input.
	if err := os.WriteFile(filepath.Join(workspace, "safepatch.json"), []byte(`{"edition":"enterprise","features":{"runtime.replay":true},"prompt":"print PASS"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "untrusted-secret-fixture"} {
		cmd := exec.CommandContext(ctx, binary, "entitlement", "show", "--json")
		cmd.Dir = workspace
		// Deliberately no inherited account credentials, config, home or proxy
		// settings. Invalid proxies plus dependency inspection establish this path
		// does not depend on a reachable service. No firewall claim is made.
		cmd.Env = []string{"SystemRoot=" + os.Getenv("SystemRoot"), "HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1", "HOME=" + filepath.Join(workspace, "absent-home"), "USERPROFILE=" + filepath.Join(workspace, "absent-profile"), "SAFEPATCH_EDITION=enterprise", "SAFEPATCH_TOKEN=" + token, "STRIPE_SECRET_KEY=" + token}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil || stderr.Len() != 0 {
			t.Fatalf("offline entitlement command: %v %s", err, stderr.String())
		}
		var report entitlement.CommunityReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.BundleVersion != "community-2026-v2" || report.NetworkRequired || report.AccountRequired {
			t.Fatal(report)
		}
		for _, f := range report.Features {
			if f.Key == "runtime.replay" && f.Entitled {
				t.Fatal("untrusted config elevated local entitlement")
			}
		}
		if strings.Contains(stdout.String(), "untrusted-secret-fixture") {
			t.Fatal("credential exposed")
		}
		for _, tc := range []struct {
			args      []string
			exit      int
			errorCode string
		}{
			{[]string{"edition", "--json"}, 0, ""},
			{[]string{"whoami", "--json"}, 0, ""},
			{[]string{"entitlement", "explain", "verify.basic", "--json"}, 0, ""},
			{[]string{"entitlement", "explain", "runtime.replay", "--json"}, 1, "FEATURE_NOT_ENTITLED"},
			{[]string{"entitlement", "explain", "proof.planner", "--json"}, 1, "FEATURE_NOT_ENTITLED"},
			{[]string{"entitlement", "explain", "unknown.feature", "--json"}, 2, "UNKNOWN_FEATURE"},
			{[]string{"whoami", "--token=untrusted-secret-fixture", "--json"}, 2, "INVALID_USAGE"},
			{[]string{"verify", "--json"}, 1, "NOT_IMPLEMENTED"},
		} {
			run := exec.CommandContext(ctx, binary, tc.args...)
			run.Dir = workspace
			run.Env = append(append([]string{}, cmd.Env...), "CI=true")
			var success, failure bytes.Buffer
			run.Stdout, run.Stderr = &success, &failure
			err := run.Run()
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.exit {
				t.Fatal(tc.args, code, failure.String())
			}
			if code == 0 {
				if failure.Len() != 0 || !json.Valid(success.Bytes()) {
					t.Fatal(tc.args, success.String(), failure.String())
				}
				if tc.args[0] == "whoami" {
					var identity map[string]json.RawMessage
					if err := json.Unmarshal(success.Bytes(), &identity); err != nil {
						t.Fatal(err)
					}
					if string(identity["authenticated"]) != "false" || string(identity["actor"]) != "null" || string(identity["organization"]) != "null" {
						t.Fatal("hostile configuration fabricated identity", identity)
					}
				}
			} else {
				if success.Len() != 0 {
					t.Fatal("error mixed with success output", tc.args)
				}
				var envelope struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(failure.Bytes(), &envelope); err != nil || envelope.Error.Code != tc.errorCode {
					t.Fatal(tc.args, err, failure.String())
				}
			}
			all := strings.ToLower(success.String() + failure.String())
			for _, banned := range []string{"untrusted-secret-fixture", "upgrade", "subscribe", "buy now", "\x1b"} {
				if strings.Contains(all, banned) {
					t.Fatal("unsafe or promotional CI output", tc.args, banned)
				}
			}
		}
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 1 || entries[0].Name() != "safepatch.json" {
		t.Fatal("inspection unexpectedly wrote local state", err, entries)
	}
}

func assertUnavailable(t *testing.T, output []byte, err error) {
	t.Helper()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 1 || !strings.Contains(string(output), "not implemented yet") {
		t.Fatalf("expected unavailable exit 1, got %v\n%s", err, output)
	}
}
