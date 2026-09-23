package runner

import (
	"os"
	"path/filepath"
	"testing"

	"safepatch/internal/verification"
)

func TestTrustedSearchPathCannotBeOverriddenByRequests(t *testing.T) {
	executable, _ := os.Executable()
	directory := filepath.Dir(executable)
	c, err := configure(Config{TemporaryDirectory: t.TempDir(), Tools: map[string]string{"tool": executable}, TrustedSearchPath: []string{directory}}, false)
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.environmentFor(verification.ExecutionRequest{})
	if err != nil || len(env) != 1 || env[0] != "PATH="+directory {
		t.Fatal(env, err)
	}
	if _, err := c.environmentFor(verification.ExecutionRequest{Environment: map[string]string{"PATH": "malicious"}}); err == nil {
		t.Fatal("request overwrote PATH")
	}
	for _, bad := range []string{".", "relative", directory + string(os.PathListSeparator) + "relative"} {
		if _, err := configure(Config{TemporaryDirectory: t.TempDir(), Tools: map[string]string{"tool": executable}, TrustedSearchPath: []string{bad}}, false); err == nil {
			t.Fatal("unsafe trusted search path", bad)
		}
	}
}
