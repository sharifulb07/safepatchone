// Package evidencestore implements restricted immutable original-evidence storage.
package evidencestore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"safepatch/internal/evidence"
)

// Local binds one already-authorized scope to a private directory. Construct a
// separate handle for each scope; a handle cannot read another tenant/revision.
// The administrator must own the root and its parents and protect it with OS
// ACLs. It must be outside repositories and inaccessible to scanner processes.
type Local struct {
	root  *os.Root
	scope evidence.ArchiveScope
}

func NewLocal(directory string, scope evidence.ArchiveScope) (*Local, error) {
	if scope.Validate() != nil || !filepath.IsAbs(directory) || runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return nil, evidence.ErrOriginalEvidence
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, evidence.ErrOriginalEvidence
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	defer root.Close()
	// Hash components to avoid filesystem aliases, Windows device names and
	// leaking tenant identities through object names. Separate scoped Roots
	// also prohibit relative symlinks into a neighboring tenant namespace.
	key := evidence.Digest([]byte(scope.OrganizationID + "\x00" + scope.Subject.RepositoryID + "\x00" + scope.Subject.Revision))
	if err := root.Mkdir(key, 0700); err != nil && !os.IsExist(err) {
		return nil, evidence.ErrOriginalEvidence
	}
	info, err = root.Lstat(key)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, evidence.ErrOriginalEvidence
	}
	bound, err := root.OpenRoot(key)
	if err != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	return &Local{root: bound, scope: scope}, nil
}
func (s *Local) Close() error { return s.root.Close() }
func (s *Local) valid(ctx context.Context, scope evidence.ArchiveScope) bool {
	return s != nil && s.root != nil && ctx != nil && ctx.Err() == nil && scope == s.scope
}

func (s *Local) Put(ctx context.Context, scope evidence.ArchiveScope, raw []byte) (evidence.OriginalReference, error) {
	ref := evidence.OriginalReference{SHA256: evidence.Digest(raw), Bytes: int64(len(raw))}
	if !s.valid(ctx, scope) || ref.Validate() != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	if _, err := s.Get(ctx, scope, ref); err == nil {
		return ref, nil
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	temp := "pending-" + hex.EncodeToString(nonce[:])
	file, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	defer s.root.Remove(temp)
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || ctx.Err() != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	// Hard-link publication is atomic and never replaces an existing object.
	// Concurrent writers either publish or verify the already-published bytes.
	if err := s.root.Link(temp, ref.SHA256); err != nil && !os.IsExist(err) {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	if _, err := s.Get(ctx, scope, ref); err != nil {
		return evidence.OriginalReference{}, err
	}
	if runtime.GOOS != "windows" {
		dir, err := s.root.Open(".")
		if err != nil {
			return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
		}
	}
	return ref, nil
}

func (s *Local) Get(ctx context.Context, scope evidence.ArchiveScope, ref evidence.OriginalReference) ([]byte, error) {
	if !s.valid(ctx, scope) || ref.Validate() != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	info, err := s.root.Lstat(ref.SHA256)
	if err != nil || !info.Mode().IsRegular() || info.Size() != ref.Bytes {
		return nil, evidence.ErrOriginalEvidence
	}
	f, err := s.root.Open(ref.SHA256)
	if err != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != ref.Bytes {
		return nil, evidence.ErrOriginalEvidence
	}
	raw, err := io.ReadAll(io.LimitReader(f, ref.Bytes+1))
	if err != nil || ctx.Err() != nil || int64(len(raw)) != ref.Bytes || evidence.Digest(raw) != ref.SHA256 {
		return nil, evidence.ErrOriginalEvidence
	}
	return raw, nil
}
