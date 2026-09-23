package licensing

import (
	"context"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

// LicensedQuotaStore derives admission limits from the currently verified license
// while preserving the existing quota_usage ledger and reservation semantics.
// It is a dedicated self-hosted license policy source, not a multi-source merge.
type LicensedQuotaStore struct {
	provider *EnterpriseLicenseEntitlementProvider
	ledger   entitlement.QuotaStore
}

func NewLicensedQuotaStore(p *EnterpriseLicenseEntitlementProvider, ledger entitlement.QuotaStore) (*LicensedQuotaStore, error) {
	if p == nil || ledger == nil {
		return nil, ErrInvalidLicense
	}
	return &LicensedQuotaStore{p, ledger}, nil
}
func (s *LicensedQuotaStore) WithinOrganization(ctx context.Context, org string, fn func(entitlement.QuotaTransaction) error) error {
	if s == nil || s.provider == nil || s.ledger == nil || ctx == nil || fn == nil || org != s.provider.org {
		return ErrInvalidLicense
	}
	return s.ledger.WithinOrganization(ctx, org, func(tx entitlement.QuotaTransaction) error { return fn(licensedTransaction{tx, s.provider, ctx, org}) })
}

type licensedTransaction struct {
	entitlement.QuotaTransaction
	provider *EnterpriseLicenseEntitlementProvider
	ctx      context.Context
	org      string
}

func (t licensedTransaction) Limit(key domain.LimitKey) (domain.EntitlementLimit, error) {
	return t.provider.ResourceLimit(t.ctx, t.org, key)
}

// Settlement deliberately uses the original ledger even after expiry/revocation.
// Releasing capacity or committing an existing reservation grants no new work.
