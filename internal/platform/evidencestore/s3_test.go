package evidencestore_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"safepatch/internal/domain/edition"
	"safepatch/internal/evidence"
	"safepatch/internal/platform/evidencestore"
	"sync"
	"testing"
)

type objectTransport struct {
	mu     sync.Mutex
	data   map[string][]byte
	calls  int
	poison bool
}

func (s *objectTransport) PutIfAbsent(_ context.Context, bucket, key string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	k := bucket + "/" + key
	if _, ok := s.data[k]; ok {
		return evidencestore.ErrObjectExists
	}
	s.data[k] = bytes.Clone(raw)
	return nil
}
func (s *objectTransport) Get(_ context.Context, bucket, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	raw, ok := s.data[bucket+"/"+key]
	if !ok {
		return nil, errors.New("missing")
	}
	if s.poison {
		raw = []byte("other tenant object")
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(raw))), nil
}
func TestS3TenantNamespacesAcrossAllEditionPairs(t *testing.T) {
	for _, a := range edition.Editions() {
		for _, b := range edition.Editions() {
			t.Run(a.Name+"_to_"+b.Name, func(t *testing.T) {
				transport := &objectTransport{data: map[string][]byte{}}
				aScope := evidence.ArchiveScope{OrganizationID: "a_" + string(a.Key), Subject: evidence.Subject{RepositoryID: "same_repo", Revision: "same_revision"}}
				bScope := aScope
				bScope.OrganizationID = "b_" + string(b.Key)
				sa, _ := evidencestore.NewS3(transport, "private-evidence", aScope)
				sb, _ := evidencestore.NewS3(transport, "private-evidence", bScope)
				ref, err := sb.Put(t.Context(), bScope, []byte("private evidence"))
				if err != nil {
					t.Fatal(err)
				}
				before := transport.calls
				if _, err := sa.Get(t.Context(), bScope, ref); err == nil || transport.calls != before {
					t.Fatal("foreign scope reached S3")
				}
				if _, err := sa.Get(t.Context(), aScope, ref); err == nil {
					t.Fatal("cross tenant digest lookup")
				}
				if _, err := sa.Put(t.Context(), aScope, []byte("private evidence")); err != nil {
					t.Fatal(err)
				}
				if len(transport.data) != 2 {
					t.Fatal("cross tenant dedup")
				}
				transport.poison = true
				if _, err := sb.Get(t.Context(), bScope, ref); err == nil {
					t.Fatal("poisoned S3 response accepted")
				}
			})
		}
	}
}
func TestS3RejectsUnsafeScopesAndBucketInput(t *testing.T) {
	tr := &objectTransport{data: map[string][]byte{}}
	scope := evidence.ArchiveScope{OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: "revision"}}
	for _, bucket := range []string{"https://attacker", "../other", "bucket/path", "127.0.0.1"} {
		if _, err := evidencestore.NewS3(tr, bucket, scope); err == nil {
			t.Fatal(bucket)
		}
	}
	s, _ := evidencestore.NewS3(tr, "private-evidence", scope)
	bad := scope
	bad.OrganizationID = "../org"
	if _, err := s.Put(t.Context(), bad, []byte("x")); err == nil {
		t.Fatal("path scope accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Put(ctx, scope, []byte("x")); err == nil {
		t.Fatal("cancelled write")
	}
	if tr.calls != 0 {
		t.Fatal("invalid input reached storage")
	}
}
