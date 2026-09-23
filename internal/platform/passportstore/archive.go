// Package passportstore supplies infrastructure for the shared Passport format.
package passportstore

import (
	"context"
	"crypto/ed25519"

	"safepatch/internal/domain/organization"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
)

// Archive reuses the tenant-scoped immutable object store. Authentication,
// permissions and retention admission must precede hosted reads and writes.
// There are no URLs to follow and no archive extraction or executable content.
type Archive struct{ Store evidence.OriginalStore }

func (a Archive) Put(ctx context.Context, p passport.Passport) (evidence.OriginalReference, error) {
	f, err := p.Fields()
	if ctx == nil || a.Store == nil || err != nil {
		return evidence.OriginalReference{}, passport.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return evidence.OriginalReference{}, err
	}
	raw, err := p.MarshalJSON()
	if err != nil {
		return evidence.OriginalReference{}, err
	}
	scope := evidence.ArchiveScope{OrganizationID: f.Change.OrganizationID, Subject: evidence.Subject{RepositoryID: f.Change.RepositoryID, Revision: f.Commit}}
	ref, err := a.Store.Put(ctx, scope, raw)
	if err != nil || ref.Validate() != nil || ref.SHA256 != p.Digest() || ref.Bytes != int64(len(raw)) || ctx.Err() != nil {
		return evidence.OriginalReference{}, evidence.ErrOriginalEvidence
	}
	return ref, nil
}

func (a Archive) Get(ctx context.Context, scope evidence.ArchiveScope, ref evidence.OriginalReference) (passport.Passport, error) {
	if ctx == nil || a.Store == nil || scope.Validate() != nil || ref.Validate() != nil || ref.Bytes > passport.MaxBytes {
		return passport.Passport{}, passport.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return passport.Passport{}, err
	}
	raw, err := a.Store.Get(ctx, scope, ref)
	if err != nil || int64(len(raw)) != ref.Bytes || evidence.Digest(raw) != ref.SHA256 || ctx.Err() != nil {
		return passport.Passport{}, evidence.ErrOriginalEvidence
	}
	p, err := passport.Decode(raw)
	if err != nil {
		return passport.Passport{}, err
	}
	f, _ := p.Fields()
	if f.Change.OrganizationID != scope.OrganizationID || f.Change.RepositoryID != scope.Subject.RepositoryID || f.Commit != scope.Subject.Revision {
		return passport.Passport{}, evidence.ErrOriginalEvidence
	}
	return p, nil
}

// Ed25519Signer is bound to one organization and key ID by trusted deployment
// configuration. Key material is copied, never serialized, fetched or logged.
type Ed25519Signer struct {
	organizationID, keyID string
	key                   ed25519.PrivateKey
}

func NewEd25519Signer(organizationID, keyID string, key ed25519.PrivateKey) (*Ed25519Signer, error) {
	if !organization.ValidID(organizationID) || !evidence.ValidIdentifier(keyID) || len(key) != ed25519.PrivateKeySize {
		return nil, passport.ErrSignature
	}
	return &Ed25519Signer{organizationID: organizationID, keyID: keyID, key: append(ed25519.PrivateKey{}, key...)}, nil
}

func (s *Ed25519Signer) Sign(ctx context.Context, p passport.Passport) (passport.Passport, error) {
	f, err := p.Fields()
	if s == nil || ctx == nil || err != nil || f.Change.OrganizationID != s.organizationID {
		return passport.Passport{}, passport.ErrSignature
	}
	if err := ctx.Err(); err != nil {
		return passport.Passport{}, err
	}
	return passport.Sign(p, s.keyID, s.key)
}
