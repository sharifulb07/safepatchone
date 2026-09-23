package entitlement

import (
	"context"
	"errors"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

// LocalOrganizationID is a process-local namespace for interface compatibility,
// NOT an account, registered organization or proof of remote tenant membership.
// Never use this provider as an error fallback for a hosted organization's grants.
const LocalOrganizationID = "local-cli"

var ErrInvalidCommunityProvider = errors.New("community entitlement provider is not configured")

// CommunityEntitlementProvider resolves the compiled, perpetual local bundle.
// It has no account, license, subscription, environment, disk, network or billing
// input and accepts only LocalOrganizationID. It grants ownership, not actor
// permissions or runtime readiness. Dependencies and grants are immutable.
type CommunityEntitlementProvider struct {
	resolver *EntitlementResolver
	catalog  *feature.FeatureCatalog
	bundle   edition.Bundle
}

var _ EntitlementService = (*CommunityEntitlementProvider)(nil)

func NewCommunityEntitlementProvider() (*CommunityEntitlementProvider, error) {
	registered := edition.CommunityBundleRegisteredAt()
	// This is reproducible catalog registration metadata, not this binary's
	// software version. No wall clock or online freshness check can expire the
	// immutable perpetual defaults. Time-bound remote grants use other providers.
	catalog, err := feature.NewBuiltinCatalog("1.0.0", registered)
	if err != nil {
		return nil, err
	}
	bundle, err := edition.NewCommunityBundle(catalog)
	if err != nil {
		return nil, err
	}
	frozen := frozenSnapshotRepository{org: LocalOrganizationID, records: make(map[string][]domain.Entitlement)}
	for _, mapping := range bundle.Features {
		record, err := domain.NewEntitlement(domain.Entitlement{
			EntitlementID: "local_" + mapping.FeatureID, OrganizationID: LocalOrganizationID,
			FeatureID: mapping.FeatureID, Source: domain.SourceCommunityDefault,
			SourceReference: bundle.Version.EditionVersionID, Enabled: mapping.Enabled,
			LimitValue: mapping.LimitValue, StartsAt: registered, CreatedAt: registered,
		}, catalog)
		if err != nil {
			return nil, err
		}
		frozen.records[record.FeatureID] = []domain.Entitlement{record}
	}
	resolver, err := NewEntitlementResolver(frozen, catalog, edition.CommunityBundleRegisteredAt)
	if err != nil {
		return nil, err
	}
	return &CommunityEntitlementProvider{resolver: resolver, catalog: catalog, bundle: bundle}, nil
}

func (p *CommunityEntitlementProvider) validate(ctx context.Context, org string) error {
	if p == nil || p.resolver == nil || p.catalog == nil {
		return ErrInvalidCommunityProvider
	}
	if ctx == nil {
		return ErrInvalidQuery
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if org != LocalOrganizationID {
		return ErrOrganizationMismatch
	}
	return nil
}

func (p *CommunityEntitlementProvider) Has(ctx context.Context, org string, key feature.Key) (bool, error) {
	if err := p.validate(ctx, org); err != nil {
		return false, err
	}
	return p.resolver.Has(ctx, org, key)
}

// Limit preserves the shared feature limit contract: unspecified is an error,
// never implicitly unlimited. This provider invents no numeric resource quotas.
func (p *CommunityEntitlementProvider) Limit(ctx context.Context, org string, key feature.Key) (int64, error) {
	if err := p.validate(ctx, org); err != nil {
		return 0, err
	}
	return p.resolver.Limit(ctx, org, key)
}

func (p *CommunityEntitlementProvider) Resolve(ctx context.Context, org string, key feature.Key) (Resolution, error) {
	if err := p.validate(ctx, org); err != nil {
		return failed("invalid_local_scope"), err
	}
	return p.resolver.Resolve(ctx, org, key)
}

// Bundle returns an owned, validated copy suitable for inspection, never a
// writable grant store. Mutating it cannot change this provider or another one.
func (p *CommunityEntitlementProvider) Bundle(ctx context.Context) (edition.Bundle, error) {
	if err := p.validate(ctx, LocalOrganizationID); err != nil {
		return edition.Bundle{}, err
	}
	return edition.NewCommunityBundle(p.catalog)
}

type CommunityFeature struct {
	Key                  feature.Key    `json:"key"`
	Entitled             bool           `json:"entitled"`
	ImplementationStatus feature.Status `json:"implementation_status"`
}

// CommunityReport describes public built-in configuration. It is not a cached
// remote entitlement snapshot, bearer credential, verification result or proof.
type CommunityReport struct {
	Edition         edition.Key        `json:"edition"`
	BundleVersion   string             `json:"bundle_version"`
	Source          domain.Source      `json:"source"`
	Scope           string             `json:"scope"`
	AccountRequired bool               `json:"account_required"`
	NetworkRequired bool               `json:"network_required"`
	Features        []CommunityFeature `json:"features"`
}

func (p *CommunityEntitlementProvider) Inspect(ctx context.Context) (CommunityReport, error) {
	if err := p.validate(ctx, LocalOrganizationID); err != nil {
		return CommunityReport{}, err
	}
	report := CommunityReport{Edition: p.bundle.Version.EditionKey, BundleVersion: p.bundle.Version.EditionVersionID, Source: domain.SourceCommunityDefault, Scope: "local", Features: make([]CommunityFeature, 0, p.catalog.Len())}
	for _, definition := range p.catalog.List() {
		owned, err := p.Has(ctx, LocalOrganizationID, definition.Key)
		if err != nil {
			return CommunityReport{}, err
		}
		report.Features = append(report.Features, CommunityFeature{Key: definition.Key, Entitled: owned, ImplementationStatus: definition.Status})
	}
	return report, nil
}
