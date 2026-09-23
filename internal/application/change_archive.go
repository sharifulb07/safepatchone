package application

import (
	"context"
	"encoding/json"
	"safepatch/internal/domain"
	"safepatch/internal/evidence"
)

// ChangeArchive reuses the same immutable evidence contract for local storage
// and private S3-compatible deployments. It never stores large blobs in SQL.
type ChangeArchive struct{ Store evidence.OriginalStore }

func (a ChangeArchive) Persist(ctx context.Context, c domain.Change, v ChangeArtifacts) (string, error) {
	if a.Store == nil || c.Validate() != nil || v.ChangeID != c.ID() {
		return "", ErrChangeWorkflow
	}
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > evidence.MaxOriginalBytes {
		return "", ErrChangeWorkflow
	}
	scope := evidence.ArchiveScope{OrganizationID: c.OrganizationID, Subject: evidence.Subject{RepositoryID: c.RepositoryID, Revision: c.HeadSHA}}
	ref, err := a.Store.Put(ctx, scope, raw)
	if err != nil || ref.Validate() != nil || ref.SHA256 != evidence.Digest(raw) || ref.Bytes != int64(len(raw)) {
		return "", ErrChangeWorkflow
	}
	return ref.SHA256, nil
}
