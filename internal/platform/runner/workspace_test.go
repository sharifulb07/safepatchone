package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"safepatch/internal/verification"
)

func TestWorkspaceBoundsAndTraversal(t *testing.T) {
	for _, mode := range []string{"bytes", "entries", "traversal", "absolute", "source contains staging", "missing", "directory missing"} {
		t.Run(mode, func(t *testing.T) {
			r, request, base := localFixture(t)
			request.Arguments = helperArgs("echo")
			if err := os.WriteFile(filepath.Join(request.SourceDirectory, "one"), []byte("12345"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(request.SourceDirectory, "two"), []byte("67890"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "bytes":
				request.Limits.InputBytes = 4
			case "entries":
				request.Limits.InputEntries = 1
			case "traversal":
				request.WorkingDirectory = "../escape"
			case "absolute":
				request.WorkingDirectory = request.SourceDirectory
			case "source contains staging":
				request.SourceDirectory = base
			case "missing":
				request.SourceDirectory = filepath.Join(request.SourceDirectory, "absent")
			case "directory missing":
				request.WorkingDirectory = "missing"
			}
			result, err := r.Run(t.Context(), request)
			if err == nil || result.Status == verification.Completed {
				t.Fatal(result, err)
			}
			if (mode == "bytes" || mode == "entries") && !errors.Is(err, verification.ErrInputLimit) {
				t.Fatal(err)
			}
			assertEmpty(t, base)
		})
	}
}

func TestWorkspaceRejectsSymlinksAndLinkedRoots(t *testing.T) {
	for _, kind := range []string{"file", "directory", "source root", "internal"} {
		t.Run(kind, func(t *testing.T) {
			r, request, base := localFixture(t)
			outside := t.TempDir()
			secret := filepath.Join(outside, "secret")
			if err := os.WriteFile(secret, []byte("secret"), 0600); err != nil {
				t.Fatal(err)
			}
			link, target := filepath.Join(request.SourceDirectory, "link"), secret
			switch kind {
			case "directory":
				target = outside
			case "source root":
				link = filepath.Join(outside, "root")
				target = request.SourceDirectory
				request.SourceDirectory = link
			case "internal":
				target = filepath.Join(request.SourceDirectory, "safe")
				if err := os.WriteFile(target, []byte("safe"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink creation unavailable: %v", err)
			}
			result, err := r.Run(t.Context(), request)
			if !errors.Is(err, verification.ErrUnsafeWorkspace) || result.Status == verification.Completed {
				t.Fatal(result, err)
			}
			actual, err := os.ReadFile(secret)
			if err != nil || string(actual) != "secret" {
				t.Fatal("outside file changed", err)
			}
			assertEmpty(t, base)
		})
	}
}

func TestCleanupDoesNotFollowReplacedWorkspace(t *testing.T) {
	_, request, base := localFixture(t)
	w, err := snapshot(t.Context(), base, request.SourceDirectory, request.Limits)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	secret := filepath.Join(outside, "keep")
	if err := os.WriteFile(secret, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(w.directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, w.directory); err != nil {
		_ = w.close()
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("cleanup escaped workspace", err)
	}
	assertEmpty(t, base)
}
