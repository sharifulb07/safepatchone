package application_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"safepatch/internal/application"
)

func TestCommandShells(t *testing.T) {
	for _, command := range []string{
		"safepatch", "safepatch-api", "safepatch-worker",
		"safepatch-collector", "safepatch-mcp-gateway", "safepatch-replay",
	} {
		t.Run(command, func(t *testing.T) {
			for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
				var out, errOut bytes.Buffer
				if code := application.Run(command, args, &out, &errOut); code != 0 {
					t.Fatalf("help exit = %d", code)
				}
				if !strings.Contains(out.String(), "Usage: "+command) || errOut.Len() != 0 {
					t.Fatalf("unexpected help: stdout=%q stderr=%q", out.String(), errOut.String())
				}
			}
			var out, errOut bytes.Buffer
			if command == "safepatch" {
				if code := application.Run(command, nil, &out, &errOut); code != 0 || !strings.Contains(out.String(), "Usage: safepatch") || errOut.Len() != 0 {
					t.Fatalf("CLI startup must show help: stdout=%q stderr=%q", out.String(), errOut.String())
				}
				return
			}
			if code := application.Run(command, nil, &out, &errOut); code != 1 {
				t.Fatalf("unimplemented command exit = %d", code)
			}
			if out.Len() != 0 || !strings.Contains(errOut.String(), "not implemented yet") {
				t.Fatalf("unimplemented command must not imply success: stdout=%q stderr=%q", out.String(), errOut.String())
			}
		})
	}
}

func TestVerificationIsUnavailable(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := application.Run("safepatch", []string{"verify"}, &out, &errOut); code != 1 {
		t.Fatalf("verify exit = %d; must not succeed", code)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "not implemented yet") {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", out.String(), errOut.String())
	}
}

func TestInvalidUsage(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"unknown", nil},
		{"safepatch", []string{"--help", "unexpected"}},
		{"safepatch", []string{"--edition", "Enterprise"}},
		{"safepatch-api", []string{"verify"}},
		{"safepatch", []string{"untrusted\nPASS"}},
	} {
		var out, errOut bytes.Buffer
		if code := application.Run(tc.command, tc.args, &out, &errOut); code != 2 {
			t.Fatalf("%q %q: exit = %d", tc.command, tc.args, code)
		}
		if out.Len() != 0 || errOut.Len() == 0 || strings.Contains(errOut.String(), "untrusted") {
			t.Fatalf("unexpected output: stdout=%q stderr=%q", out.String(), errOut.String())
		}
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestOutputFailureDoesNotSucceed(t *testing.T) {
	var errOut bytes.Buffer
	if code := application.Run("safepatch", []string{"--help"}, failingWriter{}, &errOut); code != 1 {
		t.Fatalf("failed help output exit = %d", code)
	}
	var out bytes.Buffer
	if code := application.Run("safepatch", []string{"verify"}, &out, failingWriter{}); code != 1 {
		t.Fatalf("unavailable operation with failed stderr exit = %d", code)
	}
}

func TestCLIStartupMatchesExplicitHelp(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		var args []string
		help := []string{"--help"}
		if jsonMode {
			args = []string{"--json"}
			help = append(help, "--json")
		}
		var startupOut, helpOut, stderr bytes.Buffer
		if code := application.Run("safepatch", args, &startupOut, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("startup failed: exit=%d stderr=%q", code, stderr.String())
		}
		if code := application.Run("safepatch", help, &helpOut, &stderr); code != 0 || startupOut.String() != helpOut.String() {
			t.Fatal("default startup differs from explicit help")
		}
		if code := application.Run("safepatch", args, failingWriter{}, &stderr); code != 1 {
			t.Fatal("startup output failure reported success")
		}
	}
}
