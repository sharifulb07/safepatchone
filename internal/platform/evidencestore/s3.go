package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"

	"safepatch/internal/evidence"
)

var ErrObjectExists = errors.New("immutable object exists")

// S3Objects is the trusted S3 SDK transport boundary. It must use a private
// fixed endpoint/bucket policy, authenticate requests, disable redirects, and
// implement durable conditional creation (If-None-Match: *), never overwrite.
// Credentials and public/presigned URLs are not part of evidence references.
type S3Objects interface {
	PutIfAbsent(context.Context, string, string, []byte) error
	Get(context.Context, string, string) (io.ReadCloser, error)
}
type S3 struct {
	objects S3Objects
	bucket  string
	scope   evidence.ArchiveScope
	prefix  string
}

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

func NewS3(objects S3Objects, bucket string, scope evidence.ArchiveScope) (*S3, error) {
	if objects == nil || !bucketPattern.MatchString(bucket) || scope.Validate() != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	// Independent hashes avoid delimiter/path/tenant-alias collisions. No global
	// content deduplication: identical content remains in separate tenant keys.
	prefix := "organizations/" + evidence.Digest([]byte(scope.OrganizationID)) + "/" + evidence.Digest([]byte(scope.Subject.RepositoryID+"\x00"+scope.Subject.Revision)) + "/"
	return &S3{objects, bucket, scope, prefix}, nil
}
func (s *S3) valid(ctx context.Context, scope evidence.ArchiveScope) bool {
	return s != nil && s.objects != nil && ctx != nil && ctx.Err() == nil && scope == s.scope
}
func (s *S3) Put(ctx context.Context, scope evidence.ArchiveScope, raw []byte) (evidence.OriginalReference, error) {
	if !s.valid(ctx, scope) || len(raw) > evidence.MaxOriginalBytes {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	owned := bytes.Clone(raw)
	ref := evidence.OriginalReference{SHA256: evidence.Digest(owned), Bytes: int64(len(owned))}
	err := s.objects.PutIfAbsent(ctx, s.bucket, s.prefix+ref.SHA256, owned)
	if err != nil && !errors.Is(err, ErrObjectExists) {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	if _, err = s.Get(ctx, scope, ref); err != nil {
		return evidence.OriginalReference{}, err
	}
	return ref, nil
}
func (s *S3) Get(ctx context.Context, scope evidence.ArchiveScope, ref evidence.OriginalReference) ([]byte, error) {
	if !s.valid(ctx, scope) || ref.Validate() != nil {
		return nil, evidence.ErrOriginalEvidence
	}
	r, err := s.objects.Get(ctx, s.bucket, s.prefix+ref.SHA256)
	if err != nil || r == nil {
		return nil, evidence.ErrOriginalEvidence
	}
	defer r.Close()
	raw, err := io.ReadAll(io.LimitReader(r, ref.Bytes+1))
	if err != nil || ctx.Err() != nil || int64(len(raw)) != ref.Bytes || evidence.Digest(raw) != ref.SHA256 {
		return nil, evidence.ErrOriginalEvidence
	}
	return raw, nil
}
