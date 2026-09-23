package entitlement_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/feature"
)

func validSnapshot(t *testing.T) domain.EffectiveEntitlementSnapshot {
	t.Helper()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	limit := int64(10)
	s := domain.EffectiveEntitlementSnapshot{OrganizationID: "org_a", Edition: edition.Team, EditionVersion: "team-2026-v1", Features: map[feature.Key]bool{feature.KeyVerifyBasic: true}, Limits: map[feature.Key]*int64{feature.KeyVerifyBasic: &limit}, GeneratedAt: now, ValidUntil: now.Add(time.Second), SourceVersions: domain.SourceVersions{Subscription: 1, Trial: 1, License: 1, ManualOverride: 1, EditionConfiguration: 1, CommunityDefault: 1, Promotion: 1}}
	var err error
	s.ContentHash, err = s.ComputeHash()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSnapshotIntegrityAndValidity(t *testing.T) {
	s := validSnapshot(t)
	if err := s.ValidateAt("org_a", s.GeneratedAt); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{s.GeneratedAt.Add(-time.Nanosecond), s.ValidUntil, time.Time{}} {
		if !errors.Is(s.ValidateAt("org_a", at), domain.ErrExpiredSnapshot) {
			t.Fatal("invalid time allowed")
		}
	}
	if !errors.Is(s.ValidateAt("org_b", s.GeneratedAt), domain.ErrInvalidSnapshot) {
		t.Fatal("tenant mismatch allowed")
	}
	for _, mutate := range []func(*domain.EffectiveEntitlementSnapshot){
		func(s *domain.EffectiveEntitlementSnapshot) { s.Features[feature.KeyVerifyBasic] = false },
		func(s *domain.EffectiveEntitlementSnapshot) { *s.Limits[feature.KeyVerifyBasic] = 99 },
		func(s *domain.EffectiveEntitlementSnapshot) { s.SourceVersions.ManualOverride++ },
		func(s *domain.EffectiveEntitlementSnapshot) { s.EditionVersion = "team-2027-v2" },
		func(s *domain.EffectiveEntitlementSnapshot) { s.GeneratedAt = s.GeneratedAt.Add(-time.Second) },
		func(s *domain.EffectiveEntitlementSnapshot) { s.ValidUntil = s.ValidUntil.Add(time.Second) },
	} {
		copy := s.Clone()
		mutate(&copy)
		if copy.ValidateAt("org_a", s.GeneratedAt) == nil {
			t.Fatal("tampering not detected")
		}
	}
	if err := s.ValidateAt("org_a", s.GeneratedAt); err != nil {
		t.Fatal("clone mutated original", err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip domain.EffectiveEntitlementSnapshot
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if err := roundtrip.ValidateAt("org_a", s.GeneratedAt); err != nil {
		t.Fatal("serialization lost integrity", err)
	}
	copy := s.Clone()
	copy.GeneratedAt = copy.GeneratedAt.In(time.FixedZone("test", 3600))
	copy.ValidUntil = copy.ValidUntil.In(time.FixedZone("test", 3600))
	hash, err := copy.ComputeHash()
	if err != nil || hash != s.ContentHash {
		t.Fatal("hash depends on timezone representation", err)
	}
}

func TestSnapshotRehashedInvalidStructuresStillFail(t *testing.T) {
	for _, mutate := range []func(*domain.EffectiveEntitlementSnapshot){
		func(s *domain.EffectiveEntitlementSnapshot) { s.SourceVersions.Trial = 0 },
		func(s *domain.EffectiveEntitlementSnapshot) { s.EditionVersion = "enterprise-2026-v1" },
		func(s *domain.EffectiveEntitlementSnapshot) { s.Edition = "unknown" },
		func(s *domain.EffectiveEntitlementSnapshot) { s.Limits = nil },
		func(s *domain.EffectiveEntitlementSnapshot) { s.Features = nil },
		func(s *domain.EffectiveEntitlementSnapshot) { *s.Limits[feature.KeyVerifyBasic] = -1 },
		func(s *domain.EffectiveEntitlementSnapshot) {
			s.Features[feature.KeyVerifyBasic] = false
			s.Limits[feature.KeyVerifyBasic] = nil
		},
		func(s *domain.EffectiveEntitlementSnapshot) {
			s.ValidUntil = s.GeneratedAt.Add(domain.MaxSnapshotTTL + 1)
		},
		func(s *domain.EffectiveEntitlementSnapshot) { s.ValidUntil = s.GeneratedAt },
	} {
		s := validSnapshot(t)
		mutate(&s)
		s.ContentHash, _ = s.ComputeHash()
		if s.ValidateAt("org_a", s.GeneratedAt) == nil {
			t.Fatal("malformed structure accepted")
		}
	}
}
