package evidencestore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"safepatch/internal/evidence"
)

func scope() evidence.ArchiveScope {
	return evidence.ArchiveScope{OrganizationID: "org_a", Subject: evidence.Subject{RepositoryID: "repo", Revision: "rev"}}
}

func TestLocalImmutableRoundTripAndScope(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocal(dir, scope())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := []byte(" \n{broken json\x00secret}\r\n")
	ref, err := s.Put(t.Context(), scope(), raw)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := s.Get(t.Context(), scope(), ref)
	if err != nil || !bytes.Equal(raw, actual) {
		t.Fatal("changed bytes", err)
	}
	same, err := s.Put(t.Context(), scope(), raw)
	if err != nil || same != ref {
		t.Fatal("not idempotent", err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if ref2, err := s.Put(t.Context(), scope(), []byte("concurrent")); err != nil || ref2.SHA256 != evidence.Digest([]byte("concurrent")) {
				t.Error(ref2, err)
			}
		})
	}
	wg.Wait()
	other := scope()
	other.OrganizationID = "org_b"
	if _, err := s.Get(t.Context(), other, ref); err == nil {
		t.Fatal("tenant read")
	}
	if _, err := s.Put(t.Context(), other, raw); err == nil {
		t.Fatal("tenant write")
	}
	b, err := NewLocal(dir, other)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Get(t.Context(), other, ref); err == nil {
		t.Fatal("cross-tenant object lookup")
	}
	other = scope()
	other.Subject.Revision = "rev2"
	if _, err := s.Get(t.Context(), other, ref); err == nil {
		t.Fatal("revision read")
	}
	ref.Bytes++
	if _, err := s.Get(t.Context(), scope(), ref); err == nil {
		t.Fatal("size tampering")
	}
}

func TestLocalCorruptionAndErrorPaths(t *testing.T) {
	s, err := NewLocal(t.TempDir(), scope())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ref, err := s.Put(t.Context(), scope(), []byte("original"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.root.WriteFile(ref.SHA256, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(t.Context(), scope(), ref); err == nil {
		t.Fatal("corrupt data accepted")
	}
	if _, err := s.Put(t.Context(), scope(), []byte("original")); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	for _, key := range []string{"../escape", "/absolute", "CON", ref.SHA256 + "/../a"} {
		if _, err := s.Get(t.Context(), scope(), evidence.OriginalReference{SHA256: key}); err == nil {
			t.Fatal("unsafe key")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Put(ctx, scope(), []byte("new")); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := s.Put(t.Context(), scope(), make([]byte, evidence.MaxOriginalBytes+1)); err == nil {
		t.Fatal("unbounded evidence")
	}
	if _, err := NewLocal("relative", scope()); err == nil {
		t.Fatal("relative root")
	}
	if _, err := s.Get(nil, scope(), ref); err == nil {
		t.Fatal("nil context")
	}
}

func TestLocalSymlinkEscape(t *testing.T) {
	s, err := NewLocal(t.TempDir(), scope())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw := []byte("outside")
	ref := evidence.OriginalReference{SHA256: evidence.Digest(raw), Bytes: int64(len(raw))}
	outside := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.root.Symlink(outside, ref.SHA256); err != nil {
		t.Skip("symlink creation unavailable:", err)
	}
	if _, err := s.Get(t.Context(), scope(), ref); err == nil {
		t.Fatal("followed external symlink")
	}
	if _, err := s.Put(t.Context(), scope(), raw); err == nil {
		t.Fatal("published through symlink")
	}
}
