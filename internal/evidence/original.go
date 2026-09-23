package evidence

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
)

const MaxOriginalBytes = 16 * 1024 * 1024

var ErrOriginalEvidence = errors.New("original evidence could not be preserved or verified")

// ArchiveScope is supplied by admitted orchestration, never scanner output.
// The store is not an authentication boundary. Local callers use their local
// namespace; servers must resolve and authorize the real organization first.
type ArchiveScope struct {
	OrganizationID string  `json:"organization_id"`
	Subject        Subject `json:"subject"`
}

func (s ArchiveScope) Validate() error {
	if !ValidIdentifier(s.OrganizationID) || s.Subject.Validate() != nil {
		return ErrOriginalEvidence
	}
	return nil
}

type OriginalReference struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

func (r OriginalReference) Validate() error {
	b, err := hex.DecodeString(r.SHA256)
	if err != nil || len(b) != 32 || strings.ToLower(r.SHA256) != r.SHA256 || r.Bytes < 0 || r.Bytes > MaxOriginalBytes {
		return ErrOriginalEvidence
	}
	return nil
}

// OriginalStore preserves exact bytes as immutable, scope-local objects. Put
// succeeds only after durable storage; Get verifies length and digest. Cloud
// implementations must apply the same contract with private S3-compatible
// storage. No public URLs, credentials or scanner-controlled keys enter it.
type OriginalStore interface {
	Put(context.Context, ArchiveScope, []byte) (OriginalReference, error)
	Get(context.Context, ArchiveScope, OriginalReference) ([]byte, error)
}
