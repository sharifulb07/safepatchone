package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/proof/testtrust"
	"safepatch/internal/tenant"
)

type trustSource struct {
	mode      string
	calls     int
	snapshots map[string]testtrust.Snapshot
}

func (s *trustSource) Load(_ context.Context, scope tenant.Scope, revision string) (testtrust.Snapshot, error) {
	s.calls++
	if s.mode == "source_error" {
		return testtrust.Snapshot{}, errors.New("private credential")
	}
	f, _ := s.snapshots[revision].Fields()
	switch s.mode {
	case "foreign_org":
		f.OrganizationID = "foreign"
	case "foreign_repo":
		f.Subject.RepositoryID = "foreign"
	case "wrong_revision":
		f.Subject.Revision = "foreign"
	}
	return testtrust.New(f)
}
func trustSnapshots() map[string]testtrust.Snapshot {
	out := map[string]testtrust.Snapshot{}
	hash := evidence.Digest([]byte("fixture"))
	for _, revision := range []string{"base", "head"} {
		s, _ := testtrust.New(testtrust.Fields{Schema: "safepatch.testtrust-snapshot.v1", OrganizationID: "org", Subject: evidence.Subject{RepositoryID: "repo", Revision: revision}, Producer: "fixture", ProducerVersion: "1.0.0", SourceDigest: hash, Complete: true, Tests: []testtrust.Test{{ID: "test", Source: evidence.SourceLocation{Path: "a_test.go"}, Complete: true, Skip: "ENABLED", Negative: "POSITIVE", Assertions: []testtrust.Assertion{{ID: "a", SubjectDigest: hash, PredicateDigest: hash, Relation: "EQ", Bound: "1"}}}}, Coverage: testtrust.Coverage{Known: true, BasisDigest: hash, Covered: 1, Total: 1}})
		out[revision] = s
	}
	return out
}
func TestTestTrustAdmissionAndSourceBinding(t *testing.T) {
	for _, mode := range []string{"allowed", "tenant", "not_entitled", "entitlement_error", "not_permitted", "quota", "source_error", "foreign_org", "foreign_repo", "wrong_revision", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			source := &trustSource{mode: mode, snapshots: trustSnapshots()}
			permissionCalls := 0
			service, err := application.NewTestTrustService(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				if mode == "tenant" {
					s.OrganizationID = "foreign"
				}
				return s, nil
			}), entitlementFunc(func(_ context.Context, org string, k feature.Key) (bool, error) {
				if org != "org" || k != feature.KeyVerifyBasic {
					t.Fatal("wrong base capability")
				}
				if mode == "entitlement_error" {
					return true, errors.New("private credential")
				}
				return mode != "not_entitled", nil
			}), permissionFunc(func(_ context.Context, _ authorization.Principal, _ string, k authorization.Key) (bool, error) {
				permissionCalls++
				if k != authorization.KeyProofExecute {
					t.Fatal("wrong permission")
				}
				return mode != "not_permitted", nil
			}), quotaFunc(func(context.Context, application.Execution) error {
				if mode == "quota" {
					return errors.New("private quota")
				}
				return nil
			}), source)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			r, err := service.Compare(ctx, authorization.Principal{ID: "owner", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, "base", "head")
			if mode == "allowed" {
				if err != nil || r.Decision != testtrust.NoRegression || source.calls != 2 {
					t.Fatal(r, err)
				}
				return
			}
			if err == nil || r.Decision != testtrust.Unknown || bytes.Contains([]byte(err.Error()), []byte("private")) {
				t.Fatal(r, err)
			}
			if mode == "not_entitled" && permissionCalls != 0 {
				t.Fatal("permission before entitlement")
			}
			if (mode == "tenant" || mode == "not_entitled" || mode == "not_permitted" || mode == "quota" || mode == "cancel") && source.calls != 0 {
				t.Fatal("loaded before admission")
			}
		})
	}
}
func TestTestTrustSharedAcrossEditionsAndGrantChanges(t *testing.T) {
	var reference []byte
	for _, ed := range edition.Editions() {
		t.Run(ed.Name, func(t *testing.T) {
			enabled, permitted := true, true
			source := &trustSource{snapshots: trustSnapshots()}
			original, _ := json.Marshal(source.snapshots)
			service, _ := application.NewTestTrustService(tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
				return s, nil
			}), entitlementFunc(func(context.Context, string, feature.Key) (bool, error) { return enabled, nil }), permissionFunc(func(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
				return permitted, nil
			}), quotaFunc(func(context.Context, application.Execution) error { return nil }), source)
			compare := func() (testtrust.Result, error) {
				return service.Compare(t.Context(), authorization.Principal{ID: "owner", Kind: authorization.User}, tenant.Scope{OrganizationID: "org", ResourceID: "repo"}, "base", "head")
			}
			r, err := compare()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(r)
			if reference == nil {
				reference = raw
			}
			if !bytes.Equal(reference, raw) {
				t.Fatal("edition-specific schema or execution")
			}
			enabled = false
			if r, err := compare(); err == nil || r.Decision != testtrust.Unknown {
				t.Fatal("revoked capability allowed")
			}
			enabled = true
			permitted = false
			if r, err := compare(); err == nil || r.Decision != testtrust.Unknown {
				t.Fatal("permission bypass")
			}
			permitted = true
			r, err = compare()
			if err != nil || r.Decision != testtrust.NoRegression {
				t.Fatal(r, err)
			}
			after, _ := json.Marshal(source.snapshots)
			if !bytes.Equal(original, after) {
				t.Fatal("plan change corrupted snapshots")
			}
		})
	}
}
