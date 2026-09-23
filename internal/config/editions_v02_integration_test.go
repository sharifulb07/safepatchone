package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/quotastore"
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
)

type matrixRepository struct {
	entitlement.EntitlementRepository
	records []domain.Entitlement
}

func (r matrixRepository) List(ctx context.Context, org string, q entitlement.ListQuery) (entitlement.Page, error) {
	if err := entitlement.ValidateList(org, q); err != nil {
		return entitlement.Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return entitlement.Page{}, err
	}
	page := entitlement.Page{Entitlements: []domain.Entitlement{}}
	for _, record := range r.records {
		if record.OrganizationID == org && record.FeatureID == q.FeatureID && record.EntitlementID > q.AfterID {
			page.Entitlements = append(page.Entitlements, record.Clone())
		}
	}
	return page, nil
}

type matrixIdentity struct{}

func (matrixIdentity) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if s.OrganizationID != "org" || s.ResourceID != "repo" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return s, nil
}
func (matrixIdentity) GetMembership(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
	if org != "org" {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	return authorization.Membership{OrganizationID: org, Principal: p, Role: authorization.RoleOwner, Active: p.ID == "owner"}, nil
}

type matrixUnmetered struct{}

func (matrixUnmetered) Check(context.Context, application.Execution) error { return nil }

func TestV02GrantsUseResolverAndIndependentPermissions(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", now)
	m, _ := V02Editions(fc)
	permissions, _ := authorization.NewPermissionService(matrixIdentity{})
	for index, entry := range m.Editions {
		t.Run(string(entry.Bundle.Version.EditionKey), func(t *testing.T) {
			repository := matrixRepository{}
			for _, mapping := range entry.Bundle.Features {
				record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "grant_" + mapping.FeatureID, OrganizationID: "org", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: entry.Bundle.Version.EditionVersionID, Enabled: mapping.Enabled, StartsAt: now, CreatedAt: now, Metadata: mapping.ConfigurationJSON}, fc)
				if err != nil {
					t.Fatal(err)
				}
				repository.records = append(repository.records, record)
			}
			resolver, _ := entitlement.NewEntitlementResolver(repository, fc, func() time.Time { return now })
			for _, test := range []struct {
				key   feature.Key
				first int
			}{
				{feature.KeyPolicyLocalBasic, 0}, {feature.KeyPolicyLocalAdvanced, 1}, {feature.KeyProofCloudHistory, 1}, {feature.KeyPolicyCentral, 2}, {feature.KeyProofShared, 2}, {feature.KeyEnterpriseAudit, 4}, {feature.KeyEnterpriseSelfHosted, 4},
			} {
				guard, err := application.NewExecutionGuard(matrixIdentity{}, resolver, permissions, matrixUnmetered{}, application.Requirement{Feature: test.key, Permission: authorization.KeyPolicyManage})
				if err != nil {
					t.Fatal(err)
				}
				for _, actor := range []string{"owner", "inactive"} {
					_, err := guard.Authorize(t.Context(), authorization.Principal{ID: actor, Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
					want := error(nil)
					if index < test.first {
						want = entitlement.ErrNotEntitled
					} else if actor == "inactive" {
						want = application.ErrPermissionDenied
					}
					if !errors.Is(err, want) {
						t.Fatal(test.key, actor, err, want)
					}
				}
				if owned, err := resolver.Has(t.Context(), "other", test.key); err != nil || owned {
					t.Fatal("cross-tenant grant", test.key, owned, err)
				}
			}
		})
	}
}

func TestV02LimitDowngradePreservesUsageAndPolicy(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	fc, _ := feature.NewBuiltinCatalog("0.2.0", now)
	m, _ := V02Editions(fc)
	store, _ := quotastore.NewMemory(2, 20)
	service, _ := entitlement.NewQuotaService(store, func() time.Time { return now })
	apply := func(entry EditionConfiguration) {
		for _, limit := range entry.Limits {
			if err := store.SetLimit(t.Context(), "org", limit); err != nil {
				t.Fatal(err)
			}
		}
	}
	apply(m.Editions[3])
	reservation, err := service.Reserve(t.Context(), "org", domain.LimitRepositories, 51, "existing_repositories")
	if err != nil {
		t.Fatal(err)
	}
	p, err := policy.Decode([]byte(`{"schema":"safepatch.policy.v1","policy_id":"required","tenant_id":"org","version":"1","rules":[{"id":"risk","when":{},"require":{"max_risk_score":20}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	hash := p.Hash()
	risk := 30
	in := policy.Input{TenantID: "org", RepositoryID: "repo", ChangeID: "change", RunID: "run", AsOf: now, Criticality: policy.CriticalityUnknown, RiskScore: &risk}
	before, err := (policy.Engine{}).Evaluate(t.Context(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	apply(m.Editions[2])
	if err := service.Check(t.Context(), "org", domain.LimitRepositories, 1); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal("downgrade reset usage", err)
	}
	committed, err := service.Commit(t.Context(), "org", reservation.ID)
	if err != nil || committed.Amount != 51 {
		t.Fatal("lost existing reservation", committed, err)
	}
	apply(m.Editions[3])
	if err := service.Check(t.Context(), "org", domain.LimitRepositories, 149); err != nil {
		t.Fatal(err)
	}
	if err := service.Check(t.Context(), "org", domain.LimitRepositories, 150); !errors.Is(err, entitlement.ErrQuotaExceeded) {
		t.Fatal("upgrade erased consumption", err)
	}
	after, err := (policy.Engine{}).Evaluate(t.Context(), p, in)
	a, _ := json.Marshal(after)
	b, _ := json.Marshal(before)
	current, _ := json.Marshal(p)
	if err != nil || !bytes.Equal(a, b) || !bytes.Equal(raw, current) || p.Hash() != hash {
		t.Fatal("limit change changed policy", err)
	}
}
