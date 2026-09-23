package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/release"
	"safepatch/internal/tenant"
)

type channelSource struct {
	calls int
	last  application.Release
	data  []byte
	err   error
}

func (s *channelSource) Open(_ context.Context, r application.Release, _ string) (io.ReadCloser, error) {
	s.calls++
	s.last = r
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(bytes.NewReader(s.data)), nil
}

func TestEveryEditionUsesSameChannelArtifactAndSupportGuards(t *testing.T) {
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			actor := authorization.Principal{ID: "owner", Kind: authorization.User}
			org := "org_" + string(ed.Key)
			member := &platformMembers{true, authorization.RoleOwner}
			permissions, _ := authorization.NewPermissionService(member)
			lts := false
			access := application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				if p != actor || s.OrganizationID != org {
					return tenant.Scope{}, tenant.ErrInvalidScope
				}
				return s, nil
			}), Entitlements: entitlementFunc(func(_ context.Context, o string, k feature.Key) (bool, error) {
				return o == org && (k == feature.KeyVerifyBasic || (k == feature.KeyEnterpriseLTS && lts)), nil
			}), Permissions: permissions, Quotas: quotaFunc(func(context.Context, application.Execution) error { return nil })}
			now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
			var catalog []application.Release
			for _, c := range release.Channels() {
				catalog = append(catalog, application.Release{Version: "1.5.2", Channel: string(c), SupportedUntil: now.Add(time.Hour)})
			}
			before, _ := json.Marshal(catalog)
			service, err := application.NewReleaseService(access, catalog, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			source := &channelSource{data: []byte("immutable artifact")}
			artifacts, _ := application.NewReleaseArtifacts(service, source)
			open := func(channel string) error {
				body, err := artifacts.Open(t.Context(), actor, org, "1.5.2", channel, "checksums.txt")
				if err == nil {
					defer body.Close()
					raw, _ := io.ReadAll(body)
					if string(raw) != "immutable artifact" {
						t.Fatal("artifact mutated")
					}
				}
				return err
			}
			for _, c := range release.Channels() {
				if err := open(string(c)); (err == nil) != (c != release.LTS) {
					t.Fatal(c, err)
				}
				if _, err := service.SupportEligible(t.Context(), actor, org, "1.5.2", string(c)); (err == nil) != (c != release.LTS) {
					t.Fatal("support", c, err)
				}
			}
			if source.calls != 3 {
				t.Fatal("denied LTS reached private storage")
			}
			// Even Community can hold an explicit capability; Enterprise alone grants none.
			lts = true
			if err := open("LTS"); err != nil || source.last.Channel != "lts" {
				t.Fatal(err, source.last)
			}
			if _, err := service.SupportEligible(t.Context(), actor, org, "1.5.2", "lts"); err != nil {
				t.Fatal(err)
			}
			calls := source.calls
			member.role = authorization.RoleViewer
			if err := open("lts"); err == nil || source.calls != calls {
				t.Fatal("permission bypass after entitlement")
			}
			if _, err := service.SupportEligible(t.Context(), actor, org, "1.5.2", "lts"); err == nil {
				t.Fatal("support permission bypass")
			}
			member.role = authorization.RoleOwner
			if _, err := artifacts.Open(t.Context(), actor, "foreign", "1.5.2", "lts", "checksums.txt"); err == nil || source.calls != calls {
				t.Fatal("foreign organization admitted")
			}
			lts = false
			if err := open("lts"); err == nil || source.calls != calls {
				t.Fatal("downgrade opened artifact")
			}
			after, _ := json.Marshal(catalog)
			if !bytes.Equal(before, after) || string(source.data) != "immutable artifact" {
				t.Fatal("downgrade corrupted data")
			}
			lts = true
			if err := open("lts"); err != nil {
				t.Fatal("restored entitlement inaccessible", err)
			}
			// Caller mutations cannot change the sealed service catalog or its deadlines.
			catalog[3].SupportedUntil = now.Add(10 * time.Hour)
			now = now.Add(time.Hour)
			if err := open("lts"); err == nil {
				t.Fatal("expired release admitted")
			}
		})
	}
}

func TestChannelCatalogAliasesAndArtifactErrorPaths(t *testing.T) {
	actor := authorization.Principal{ID: "owner", Kind: authorization.User}
	access := application.PlatformAccess{Tenants: tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), Entitlements: entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return true, nil }), Permissions: permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
		return true, nil
	}), Quotas: quotaFunc(func(context.Context, application.Execution) error { return nil })}
	now := time.Now().UTC()
	r := application.Release{Version: "1.5.2", Channel: "LTS", SupportedUntil: now.Add(time.Hour)}
	alias := r
	alias.Channel = "lts"
	if _, err := application.NewReleaseService(access, []application.Release{r, alias}, func() time.Time { return now }); err == nil {
		t.Fatal("duplicate alias shadowed catalog")
	}
	s, _ := application.NewReleaseService(access, []application.Release{r}, func() time.Time { return now })
	for _, bad := range []string{"enterprise", "", " LTS", "Lts", "stable/../lts"} {
		if _, err := s.Eligible(t.Context(), actor, "org", "1.5.2", bad); err == nil {
			t.Fatal(bad)
		}
		badRelease := r
		badRelease.Channel = bad
		if _, err := application.NewReleaseService(access, []application.Release{badRelease}, func() time.Time { return now }); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	source := &channelSource{err: errors.New("private storage credential")}
	a, _ := application.NewReleaseArtifacts(s, source)
	for _, bad := range []string{"../key", "https://internal/secret", "a/b", "a\\b", "%2e%2e", "", strings.Repeat("x", 129)} {
		if _, err := a.Open(t.Context(), actor, "org", "1.5.2", "lts", bad); err == nil {
			t.Fatal(bad)
		}
	}
	if source.calls != 0 {
		t.Fatal("invalid names reached storage")
	}
	if _, err := a.Open(t.Context(), actor, "org", "1.5.2", "lts", "checksums.txt"); err == nil || strings.Contains(err.Error(), "credential") {
		t.Fatal("source failure leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Open(ctx, actor, "org", "1.5.2", "lts", "checksums.txt"); err == nil || source.calls != 1 {
		t.Fatal("cancelled read reached source")
	}
}
