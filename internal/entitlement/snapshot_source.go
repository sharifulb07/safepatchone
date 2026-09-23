package entitlement

import (
	"context"

	domain "safepatch/internal/domain/entitlement"
)

// SnapshotInput must be one complete, consistent, organization-scoped read of
// state and ALL records (including disabled, future, and expired records).
type SnapshotInput struct {
	State        domain.SnapshotState
	Entitlements []domain.Entitlement
}

// SnapshotSource is a trusted adapter boundary. CurrentState is a cheap,
// authoritative read, never a stale replica/cache. Load reads state and grants
// from one storage snapshot; wrapping independent repository List calls does
// not satisfy this contract. Both return owned copies and honor cancellation.
//
// Every subscription, trial, license, manual override, edition configuration or
// assignment, default, and promotion change MUST atomically advance its revision
// with the canonical record/assignment change. This includes deletion/revocation.
// Existing immutable edition versions must not be overwritten. Source labels
// do not substitute for validating payment/license/security inputs at ingestion.
type SnapshotSource interface {
	CurrentState(context.Context, string) (domain.SnapshotState, error)
	Load(context.Context, string) (SnapshotInput, error)
}

type SnapshotChange string

const (
	SubscriptionChanged         SnapshotChange = "subscription"
	TrialChanged                SnapshotChange = "trial"
	LicenseChanged              SnapshotChange = "license"
	ManualOverrideChanged       SnapshotChange = "manual_override"
	EditionConfigurationChanged SnapshotChange = "edition_configuration"
	CommunityDefaultsChanged    SnapshotChange = "community_default"
	PromotionChanged            SnapshotChange = "promotion"
)

func validSnapshotChange(change SnapshotChange) bool {
	switch change {
	case SubscriptionChanged, TrialChanged, LicenseChanged, ManualOverrideChanged, EditionConfigurationChanged, CommunityDefaultsChanged, PromotionChanged:
		return true
	default:
		return false
	}
}
