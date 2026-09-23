package evidencestore

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/intent/guard"
)

// Exercise actual Git output rather than only handwritten raw-format fixtures.
func TestIntentGuardRealGitAndImmutableEvidenceStorage(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git is required for native diff integration")
	}
	repo := t.TempDir()
	home := t.TempDir()
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), git, args...)
		cmd.Dir = repo
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(strings.SplitN(entry, "=", 2)[0]), "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(home, "empty"), "GIT_CONFIG_COUNT=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatal("Git fixture failed", err)
		}
		return out
	}
	run("init", "--quiet")
	if err := os.Mkdir(filepath.Join(repo, "payments"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "payments", "receipt.go"), []byte("package payments\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "--", "payments/receipt.go")
	// Cached diff against the empty tree requires no commit identity or hooks.
	raw := run("diff", "--cached", "--raw", "--no-abbrev", "--no-ext-diff", "--no-textconv", "-z")
	paths, err := guard.ParseGitDiff(t.Context(), raw)
	if err != nil || !slices.Equal(paths, []string{"payments/receipt.go"}) {
		t.Fatal(paths, err)
	}
	s := scope()
	c, _ := intent.NewIntentContract(intent.IntentContractFields{Goal: "Change payments only", AllowedComponents: []string{"payments"}})
	in := guard.Input{Identity: guard.Identity{TenantID: s.OrganizationID, RepositoryID: s.Subject.RepositoryID, ChangeID: "change", RunID: "run", CreatedAt: time.Now().UTC()}, Contract: c, GitDiff: guard.GitDiff{Raw: raw, Complete: true}, SemanticChanges: guard.SemanticChanges{TenantID: s.OrganizationID, RepositoryID: s.Subject.RepositoryID, ChangeID: "change", RunID: "run", DiffHash: evidence.Digest(raw), Complete: true, Deterministic: true, Changes: []guard.SemanticChange{{Path: paths[0], Component: "payments"}}}}
	r, err := (guard.IntentGuard{}).Evaluate(t.Context(), in)
	if err != nil || r.IntentCompliance.Status != evidence.StatusPass {
		t.Fatal(r, err)
	}
	store, err := NewLocal(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, node := range r.Evidence {
		raw, _ := node.CanonicalJSON()
		ref, err := store.Put(t.Context(), s, raw)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := store.Get(t.Context(), s, ref)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := evidence.DecodeEvidence(stored)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := restored.Fields()
		original, _ := node.Fields()
		if f.ContentHash != original.ContentHash || f.EvidenceID != original.EvidenceID {
			t.Fatal("changed evidence")
		}
		foreign := s
		foreign.OrganizationID = "other"
		if _, err := store.Get(t.Context(), foreign, ref); err == nil {
			t.Fatal("cross-tenant evidence read")
		}
	}
}
