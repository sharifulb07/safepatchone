package application

import (
	"context"
	"errors"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/passport"
	"safepatch/internal/tenant"
)

var ErrPassportUnavailable = errors.New("authoritative Passport unavailable")

// CompletedPassportSource resolves an immutable completed run from server-owned
// state, scoped to organization/repository. It MUST NOT accept uploaded documents,
// arbitrary blob digests, or client-supplied policy/proof claims as run output.
type CompletedPassportSource interface {
	LoadCompleted(context.Context, tenant.Scope, string) (passport.Passport, error)
}

// PassportSigner uses deployment-owned keys, optionally backed by KMS/HSM. Keys
// and key selection never come from requests or Passport contents.
type PassportSigner interface {
	Sign(context.Context, passport.Passport) (passport.Passport, error)
}

// HostedPassportService gates the signing service, not portable serialization,
// decoding, local signing, or signature verification. Retention and trust-key
// policy belong to storage/admission configuration, never Passport fields.
type HostedPassportService struct {
	access *ExecutionGuard
	source CompletedPassportSource
	signer PassportSigner
}

func NewHostedPassportService(tenants TenantResolver, grants entitlement.EntitlementService, permissions authorization.PermissionChecker, quotas QuotaChecker, source CompletedPassportSource, signer PassportSigner) (*HostedPassportService, error) {
	if source == nil || signer == nil {
		return nil, ErrInvalidGuard
	}
	access, err := NewExecutionGuard(tenants, grants, permissions, quotas, Requirement{Feature: feature.KeyProofPassport, Permission: authorization.KeyProofExecute})
	if err != nil {
		return nil, err
	}
	return &HostedPassportService{access: access, source: source, signer: signer}, nil
}

func (s *HostedPassportService) Sign(ctx context.Context, actor authorization.Principal, scope tenant.Scope, runID string) (passport.Passport, error) {
	if s == nil || !evidence.ValidIdentifier(runID) {
		return passport.Passport{}, ErrPassportUnavailable
	}
	if _, err := s.access.Authorize(ctx, actor, scope); err != nil {
		return passport.Passport{}, err
	}
	p, err := s.source.LoadCompleted(ctx, scope, runID)
	if err != nil {
		return passport.Passport{}, ErrPassportUnavailable
	}
	f, err := p.Fields()
	if err != nil || f.Change.OrganizationID != scope.OrganizationID || f.Change.RepositoryID != scope.ResourceID || f.RunID != runID {
		return passport.Passport{}, ErrPassportUnavailable
	}
	sig, err := p.Signature()
	if err != nil || sig != nil {
		return passport.Passport{}, ErrPassportUnavailable
	}
	// A source read can take time; recheck current admission before using the key.
	if _, err := s.access.authorizeAccess(ctx, actor, scope); err != nil {
		return passport.Passport{}, err
	}
	signed, err := s.signer.Sign(ctx, p)
	if err != nil || ctx.Err() != nil {
		return passport.Passport{}, ErrPassportUnavailable
	}
	// A signer may add a signature, never rewrite the verification statement.
	signedFields, err := signed.Fields()
	if err != nil {
		return passport.Passport{}, ErrPassportUnavailable
	}
	unsigned, err := passport.New(signedFields)
	sig, sigErr := signed.Signature()
	if err != nil || sigErr != nil || sig == nil || unsigned.Digest() != p.Digest() {
		return passport.Passport{}, ErrPassportUnavailable
	}
	return signed, nil
}
