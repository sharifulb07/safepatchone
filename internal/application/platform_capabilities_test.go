package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/audit"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/tenant"
)

type platformMembers struct {
	active bool
	role   authorization.Role
}

func (m *platformMembers) GetMembership(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
	return authorization.Membership{OrganizationID: org, Principal: p, Role: m.role, Active: m.active}, nil
}

type auditFixture struct {
	events []audit.Event
	err    error
}

func (s *auditFixture) Append(_ context.Context, org string, e audit.Event) error {
	if e.Validate() != nil || e.OrganizationID != org {
		return audit.ErrInvalid
	}
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, e)
	return nil
}
func (s *auditFixture) List(_ context.Context, _ string, _ audit.Query) (audit.Page, error) {
	return audit.Page{Events: append([]audit.Event{}, s.events...)}, s.err
}

func TestAllEnterpriseCapabilityBoundariesPreserveSharedData(t *testing.T) {
	keys := []feature.Key{feature.KeyEnterpriseSSO, feature.KeyEnterpriseAudit, feature.KeyEnterpriseSelfHosted, feature.KeyEnterpriseAirgap, feature.KeyEnterpriseLTS}
	for _, key := range keys {
		t.Run(string(key), func(t *testing.T) {
			actor := authorization.Principal{ID: "user", Kind: authorization.User}
			member := &platformMembers{true, authorization.RoleOwner}
			permissions, _ := authorization.NewPermissionService(member)
			grants := map[feature.Key]bool{}
			for _, k := range keys {
				grants[k] = true
			}
			grants[feature.KeyAuditOrganization] = true
			grants[feature.KeyVerifyBasic] = true
			access := application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				if s.OrganizationID != "org" || p != actor {
					return tenant.Scope{}, tenant.ErrInvalidScope
				}
				return s, nil
			}), Entitlements: entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
				return org == "org" && grants[k], nil
			}), Permissions: permissions, Quotas: quotaFunc(func(context.Context, application.Execution) error { return nil })}
			now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
			data := &auditFixture{events: []audit.Event{{ID: "event", OrganizationID: "org", Actor: actor, Action: "proof.verify", ResourceID: "repo", Outcome: "UNKNOWN", OccurredAt: now}}}
			prior, _ := json.Marshal(data.events)
			sso, err := application.NewSSOService("org", authFunc(func(context.Context, string) (authorization.Principal, error) { return actor, nil }), access)
			if err != nil {
				t.Fatal(err)
			}
			audits, err := application.NewAuditService(access, data)
			if err != nil {
				t.Fatal(err)
			}
			host, err := application.NewDeploymentAccess(access, key == feature.KeyEnterpriseAirgap)
			if err != nil {
				t.Fatal(err)
			}
			release := application.Release{Version: "1.5.2", Channel: "lts", SupportedUntil: now.Add(24 * time.Hour)}
			releases, err := application.NewReleaseService(access, []application.Release{release}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			op := func() error {
				switch key {
				case feature.KeyEnterpriseSSO:
					v, e := sso.Authenticate(t.Context(), "verified-credential")
					if e == nil && (v.Actor != actor || v.OrganizationID != "org") {
						t.Fatal("identity binding lost")
					}
					return e
				case feature.KeyEnterpriseAudit:
					v, e := audits.Export(t.Context(), actor, "org", audit.Query{Limit: 10})
					if e == nil && (len(v.Digest) != 64 || !slices.Equal(v.Page.Events, data.events)) {
						t.Fatal("export changed shared data")
					}
					return e
				case feature.KeyEnterpriseLTS:
					v, e := releases.Eligible(t.Context(), actor, "org", "1.5.2", "LTS")
					if e == nil && v != release {
						t.Fatal("version changed with edition")
					}
					return e
				default:
					return host.Authorize(t.Context(), actor, tenant.Scope{OrganizationID: "org", ResourceID: "repo"})
				}
			}
			if err := op(); err != nil {
				t.Fatal("entitled denied", err)
			}
			grants[key] = false
			if err := op(); err == nil {
				t.Fatal("non-entitled admitted")
			}
			after, _ := json.Marshal(data.events)
			if string(prior) != string(after) {
				t.Fatal("downgrade mutated history")
			}
			grants[key] = true
			member.active = false
			if err := op(); err == nil {
				t.Fatal("inactive member admitted after entitlement")
			}
			member.active = true
			if key != feature.KeyEnterpriseSSO {
				member.role = authorization.RoleViewer
				if err := op(); err == nil {
					t.Fatal("viewer performed privileged operation")
				}
				member.role = authorization.RoleOwner
			}
			if err := op(); err != nil {
				t.Fatal("restored grant inaccessible", err)
			}
		})
	}
}
func TestPlatformErrorsBaselineAuditAndStableRelease(t *testing.T) {
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	now := time.Now().UTC()
	member := &platformMembers{true, authorization.RoleOwner}
	permissions, _ := authorization.NewPermissionService(member)
	errSource := false
	access := application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), Entitlements: entitlementFunc(func(context.Context, string, feature.Key) (bool, error) {
		if errSource {
			return false, errors.New("private backend error")
		}
		return true, nil
	}), Permissions: permissions, Quotas: quotaFunc(func(context.Context, application.Execution) error { return nil })}
	data := &auditFixture{events: []audit.Event{}}
	audits, _ := application.NewAuditService(access, data)
	foreign := audit.Event{ID: "event", OrganizationID: "other", Actor: actor, Action: "proof.verify", ResourceID: "repo", Outcome: "SUCCEEDED", OccurredAt: now}
	data.events = append(data.events, foreign)
	if _, err := audits.Export(t.Context(), actor, "org", audit.Query{Limit: 10}); err == nil {
		t.Fatal("cross-tenant audit poisoning")
	}
	data.events = []audit.Event{}
	errSource = true
	if err := data.Append(t.Context(), "other", foreign); err != nil {
		t.Fatal("baseline capture entitlements coupled")
	}
	if _, err := audits.Export(t.Context(), actor, "other", audit.Query{Limit: 10}); err == nil {
		t.Fatal("entitlement error opened export")
	}
	errSource = false
	stable := application.Release{Version: "1.5.2", Channel: "stable", SupportedUntil: now.Add(time.Hour)}
	lts := stable
	lts.Channel = "LTS"
	r, err := application.NewReleaseService(access, []application.Release{stable, lts}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.Eligible(t.Context(), actor, "org", "1.5.2", "stable"); err != nil || got != stable {
		t.Fatal(got, err)
	}
	now = lts.SupportedUntil
	if _, err := r.Eligible(t.Context(), actor, "org", "1.5.2", "LTS"); err == nil {
		t.Fatal("expired support allowed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := audits.Read(ctx, actor, "other", audit.Query{Limit: 10}); err == nil {
		t.Fatal("cancelled request allowed")
	}
	if _, err := audits.Read(t.Context(), actor, "other", audit.Query{Limit: audit.MaxPage + 1}); err == nil {
		t.Fatal("unbounded export")
	}
}
func TestSSORejectsUnverifiedAndServiceAccountIdentity(t *testing.T) {
	access := application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), Entitlements: entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return true, nil }), Permissions: permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return true, nil
	}), Quotas: quotaFunc(func(context.Context, application.Execution) error { return nil })}
	for _, bad := range []string{"forged", "service"} {
		s, _ := application.NewSSOService("org", authFunc(func(context.Context, string) (authorization.Principal, error) {
			if bad == "forged" {
				return authorization.Principal{}, errors.New("invalid identity assertion")
			}
			return authorization.Principal{ID: "service", Kind: authorization.ServiceAccount}, nil
		}), access)
		if _, err := s.Authenticate(t.Context(), "credential"); err == nil {
			t.Fatal("bad SSO identity accepted")
		}
	}
}
