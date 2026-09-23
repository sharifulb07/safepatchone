package entitlement

import "errors"

// LimitKey identifies a resource, independently of feature ownership and edition.
type LimitKey string

const (
	LimitRepositories            LimitKey = "repositories"
	LimitDevelopers              LimitKey = "developers"
	LimitProofCreditsMonthly     LimitKey = "proof_credits_monthly"
	LimitVerificationConcurrency LimitKey = "verification_concurrency"
	LimitRetentionDays           LimitKey = "retention_days"
	LimitArtifactStorageBytes    LimitKey = "artifact_storage_bytes"
	LimitReplayRequestsMonthly   LimitKey = "replay_requests_monthly"
	LimitFuzzSecondsMonthly      LimitKey = "fuzz_seconds_monthly"
	LimitDatasetRetentionDays    LimitKey = "dataset_retention_days"
	LimitReplayConcurrency       LimitKey = "replay_concurrency"
	LimitSafeUpgradesMonthly     LimitKey = "safe_upgrades_monthly"
)

type LimitState string

const (
	Unlimited LimitState = "UNLIMITED"
	Integer   LimitState = "INTEGER"
	Disabled  LimitState = "DISABLED"
)

type Accounting string

const (
	Capacity Accounting = "CAPACITY"
	Monthly  Accounting = "MONTHLY"
	Ceiling  Accounting = "CEILING"
)

var ErrInvalidLimit = errors.New("invalid entitlement limit")

func LimitKeys() []LimitKey {
	return []LimitKey{LimitRepositories, LimitDevelopers, LimitProofCreditsMonthly, LimitVerificationConcurrency, LimitRetentionDays, LimitArtifactStorageBytes, LimitReplayRequestsMonthly, LimitFuzzSecondsMonthly, LimitDatasetRetentionDays, LimitReplayConcurrency, LimitSafeUpgradesMonthly}
}

func (k LimitKey) Accounting() (Accounting, error) {
	switch k {
	case LimitRepositories, LimitDevelopers, LimitVerificationConcurrency, LimitArtifactStorageBytes, LimitReplayConcurrency:
		return Capacity, nil
	case LimitProofCreditsMonthly, LimitReplayRequestsMonthly, LimitFuzzSecondsMonthly, LimitSafeUpgradesMonthly:
		return Monthly, nil
	case LimitRetentionDays, LimitDatasetRetentionDays:
		return Ceiling, nil
	default:
		return "", ErrInvalidLimit
	}
}

// EntitlementLimit is an explicit effective resource policy. The zero value and
// unknown states are invalid. INTEGER zero allows no positive consumption.
// Non-integer states require Value zero; no sentinel numbers mean unlimited.
// Existing nullable feature LimitValue fields retain their original semantics.
type EntitlementLimit struct {
	Key   LimitKey   `json:"key"`
	State LimitState `json:"state"`
	Value int64      `json:"value"`
}

func (l EntitlementLimit) Validate() error {
	if _, err := l.Key.Accounting(); err != nil {
		return err
	}
	switch l.State {
	case Integer:
		if l.Value >= 0 {
			return nil
		}
	case Unlimited, Disabled:
		if l.Value == 0 {
			return nil
		}
	}
	return ErrInvalidLimit
}
