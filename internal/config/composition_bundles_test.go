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
	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/proof/composition"
	"safepatch/internal/tenant"
)

// Missing verification evidence intentionally yields UNKNOWN, while successful
// admission is tested independently from proof success.
type compositionSource struct {
	m     composition.Manifest
	calls int
}

func (s *compositionSource) Manifest(context.Context, string, string) (composition.Manifest, error) {
	s.calls++
	return s.m, nil
}
func (s *compositionSource) Evidence(context.Context, composition.Manifest) (composition.Input, error) {
	s.calls++
	return composition.Input{}, nil
}

type compositionIdentity struct{ viewer, foreign, quota bool }

func (i *compositionIdentity) Resolve(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	if i.foreign || s.OrganizationID != "org" || (s.ResourceID != "org" && s.ResourceID != "repo") {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return s, nil
}
func (i *compositionIdentity) GetMembership(_ context.Context, org string, p authorization.Principal) (authorization.Membership, error) {
	role := authorization.RoleOwner
	if i.viewer {
		role = authorization.RoleViewer
	}
	return authorization.Membership{OrganizationID: org, Principal: p, Role: role, Active: p.ID == "owner" && org == "org"}, nil
}
func (i *compositionIdentity) Check(context.Context, application.Execution) error {
	if i.quota {
		return errors.New("quota")
	}
	return nil
}
func TestCompositionAllocationAndReassignmentNeedOnlyConfiguration(t *testing.T) {
	now := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	fc, _ := feature.NewBuiltinCatalog("1.0.0", now)
	base, _ := V02Editions(fc)
	before, _ := json.Marshal(base)
	profile, err := CompositionEditions(fc)
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range profile.Editions {
		expected := entry.Bundle.Version.EditionKey == edition.Business || entry.Bundle.Version.EditionKey == edition.Enterprise
		enabled := false
		for _, m := range entry.Bundle.Features {
			if m.FeatureID == "feat_0042" {
				enabled = m.Enabled
			}
		}
		if enabled != expected {
			t.Fatal(entry.Bundle.Version)
		}
		a, _ := json.Marshal(entry.Limits)
		b, _ := json.Marshal(base.Editions[i].Limits)
		if !bytes.Equal(a, b) {
			t.Fatal("quota templates changed")
		}
	}
	// A later Team bundle is pure data. The same loader and guard execute it.
	var updates editionUpdates
	_ = json.Unmarshal(compositionBundles, &updates)
	u := updates.Updates[0]
	u.BaseVersion = "team-2026-v2"
	u.Version.EditionKey = edition.Team
	u.Version.EditionVersionID = "team-2026-v3"
	u.Features = append([]edition.EditionFeature{}, u.Features...)
	u.Features[0].EditionVersionID = u.Version.EditionVersionID
	updates.Updates = append(updates.Updates, u)
	raw, _ := json.Marshal(updates)
	future, err := applyEditionUpdates(fc, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, matrix := range []EditionMatrix{profile, future} {
		for _, entry := range matrix.Editions {
			t.Run(entry.Bundle.Version.EditionVersionID, func(t *testing.T) {
				repo := matrixRepository{}
				enabled := false
				for _, mapping := range entry.Bundle.Features {
					record, err := domain.NewEntitlement(domain.Entitlement{EntitlementID: "grant_" + mapping.FeatureID, OrganizationID: "org", FeatureID: mapping.FeatureID, Source: domain.SourceSubscription, SourceReference: entry.Bundle.Version.EditionVersionID, Enabled: mapping.Enabled, StartsAt: now, CreatedAt: now}, fc)
					if err != nil {
						t.Fatal(err)
					}
					repo.records = append(repo.records, record)
					if mapping.FeatureID == "feat_0042" {
						enabled = mapping.Enabled
					}
				}
				resolver, _ := entitlement.NewEntitlementResolver(repo, fc, func() time.Time { return now })
				identity := &compositionIdentity{}
				permissions, _ := authorization.NewPermissionService(identity)
				m, err := composition.New(composition.Fields{Schema: composition.ManifestSchema, ID: "set", OrganizationID: "org", PolicyDigest: string(makeDigest()), Components: []composition.Component{{ID: "a", RepositoryID: "repo", Commit: makeCommit(), PassportDigest: makeDigest()}, {ID: "b", RepositoryID: "repo", Commit: makeCommit(), PassportDigest: makeDigest()}}, Connections: []composition.Connection{{ID: "api", From: "a", To: "b", ContractDigest: makeDigest()}}})
				if err != nil {
					t.Fatal(err)
				}
				source := &compositionSource{m: m}
				guard, _ := application.NewCompositionGuard(identity, resolver, permissions, identity, source, func() time.Time { return now })
				actor := authorization.Principal{ID: "owner", Kind: authorization.User}
				_, err = guard.Evaluate(t.Context(), actor, "org", "set")
				if (err == nil) != enabled {
					t.Fatal("wrong entitlement admission", err)
				}
				if !enabled {
					if source.calls != 0 {
						t.Fatal("unentitled source read")
					}
					return
				}
				history, _ := m.MarshalJSON()
				identity.viewer = true
				if _, err = guard.Evaluate(t.Context(), actor, "org", "set"); !errors.Is(err, application.ErrPermissionDenied) {
					t.Fatal(err)
				}
				identity.viewer = false
				identity.foreign = true
				if _, err = guard.Evaluate(t.Context(), actor, "org", "set"); err == nil {
					t.Fatal("tenant bypass")
				}
				identity.foreign = false
				identity.quota = true
				if _, err = guard.Evaluate(t.Context(), actor, "org", "set"); err == nil {
					t.Fatal("quota bypass")
				}
				identity.quota = false
				for i := range repo.records {
					if repo.records[i].FeatureID == "feat_0042" {
						repo.records[i].Enabled = false
					}
				}
				// New resolver models authoritative plan reassignment without rewriting the manifest.
				downgraded, _ := entitlement.NewEntitlementResolver(repo, fc, func() time.Time { return now })
				denied, _ := application.NewCompositionGuard(identity, downgraded, permissions, identity, source, func() time.Time { return now })
				if _, err = denied.Evaluate(t.Context(), actor, "org", "set"); err == nil {
					t.Fatal("downgrade bypass")
				}
				after, _ := m.MarshalJSON()
				if !bytes.Equal(history, after) {
					t.Fatal("history corrupted")
				}
			})
		}
	}
	again, _ := V02Editions(fc)
	after, _ := json.Marshal(again)
	if !bytes.Equal(before, after) {
		t.Fatal("historical profile changed")
	}
}
func makeDigest() string { return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }
func makeCommit() string { return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" }
func TestCompositionConfigurationRejectsInvalidUpdates(t *testing.T) {
	fc, _ := feature.NewBuiltinCatalog("1.0.0", time.Now())
	for _, raw := range [][]byte{[]byte(`null`), []byte(`{"schema":"safepatch.edition-updates.v1","updates":[]}`), bytes.Replace(compositionBundles, []byte("business-2026-v2"), []byte("team-2026-v2"), 1), bytes.ReplaceAll(compositionBundles, []byte("feat_0042"), []byte("missing")), append(compositionBundles, []byte(`{}`)...)} {
		if _, err := applyEditionUpdates(fc, raw); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
}
