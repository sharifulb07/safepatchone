package entitlement

import (
	"context"
	"errors"
	"math"
	"time"

	domain "safepatch/internal/domain/entitlement"
)

var (
	ErrInvalidQuota  = errors.New("invalid quota request or configuration")
	ErrQuotaDisabled = errors.New("quota is disabled")
	ErrQuotaExceeded = errors.New("quota exceeded")
	ErrQuotaConflict = errors.New("quota reservation conflicts with prior request or state")
	ErrQuotaNotFound = errors.New("quota reservation not found")
	ErrQuotaData     = errors.New("invalid quota storage data")
	ErrQuotaBound    = errors.New("quota accounting resource bound exceeded")
)

type ReservationState string

const (
	Reserved  ReservationState = "RESERVED"
	Committed ReservationState = "COMMITTED"
	Released  ReservationState = "RELEASED"
)

type Reservation struct {
	OrganizationID string           `json:"organization_id"`
	ID             string           `json:"reservation_id"`
	Key            domain.LimitKey  `json:"key"`
	Period         string           `json:"period"`
	Amount         int64            `json:"amount"`
	State          ReservationState `json:"state"`
}

type Usage struct {
	Reserved  int64
	Committed int64
}

// QuotaStore serializes ALL operations and policy updates within an organization.
// The callback runs once, with current authoritative limits, and all its writes
// commit atomically or roll back on error/cancellation/panic. Implementations
// must scope storage queries by organization, honor contexts and never silently
// evict reservation IDs. Transactions may not escape or be used concurrently.
type QuotaStore interface {
	WithinOrganization(context.Context, string, func(QuotaTransaction) error) error
}

type QuotaTransaction interface {
	// Missing policy returns an explicit DISABLED limit, never unlimited.
	Limit(domain.LimitKey) (domain.EntitlementLimit, error)
	Usage(domain.LimitKey, string) (Usage, error)
	Find(string) (Reservation, error)
	PutUsage(domain.LimitKey, string, Usage) error
	PutReservation(Reservation) error
}

// QuotaService accounts resources only: it grants neither feature ownership nor
// actor permissions. Trusted application code must authenticate, resolve tenant
// ownership, and enforce Has and Allowed before admission. Amounts and resource
// keys come from trusted operation policy, not client claims or LLM output.
type QuotaService struct {
	store QuotaStore
	now   func() time.Time
}

func NewQuotaService(store QuotaStore, now func() time.Time) (*QuotaService, error) {
	if store == nil || now == nil {
		return nil, ErrInvalidQuota
	}
	return &QuotaService{store: store, now: now}, nil
}

func (s *QuotaService) transaction(ctx context.Context, org string, f func(QuotaTransaction) error) error {
	if s == nil || s.store == nil || s.now == nil || ctx == nil || !domain.ValidID(org) {
		return ErrInvalidQuota
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.store.WithinOrganization(ctx, org, f)
}

// Check is advisory for additive quotas; only Reserve prevents admission races.
// For retention_days, amount is the requested duration, not accumulated usage.
func (s *QuotaService) Check(ctx context.Context, org string, key domain.LimitKey, amount int64) error {
	if _, err := key.Accounting(); err != nil || amount <= 0 {
		return ErrInvalidQuota
	}
	return s.transaction(ctx, org, func(tx QuotaTransaction) error {
		period, err := s.period(key)
		if err != nil {
			return err
		}
		_, err = admit(tx, key, period, amount)
		return err
	})
}

// Reserve holds amount atomically. ID is an organization-wide idempotency key
// for one logical operation, retained across periods. Reusing it with different
// arguments or after release fails. A retry returns the original state and does
// not authorize executing the underlying operation twice.
func (s *QuotaService) Reserve(ctx context.Context, org string, key domain.LimitKey, amount int64, id string) (Reservation, error) {
	kind, err := key.Accounting()
	if err != nil || kind == domain.Ceiling || amount <= 0 || !domain.ValidID(id) {
		return Reservation{}, ErrInvalidQuota
	}
	var result Reservation
	err = s.transaction(ctx, org, func(tx QuotaTransaction) error {
		prior, err := tx.Find(id)
		if err == nil {
			if err := validateReservation(prior, org, id); err != nil {
				return err
			}
			if prior.Key != key || prior.Amount != amount || prior.State == Released {
				return ErrQuotaConflict
			}
			result = prior
			return nil
		}
		if !errors.Is(err, ErrQuotaNotFound) {
			return err
		}
		period, err := s.period(key)
		if err != nil {
			return err
		}
		usage, err := admit(tx, key, period, amount)
		if err != nil {
			return err
		}
		usage.Reserved += amount
		result = Reservation{OrganizationID: org, ID: id, Key: key, Period: period, Amount: amount, State: Reserved}
		if err := tx.PutUsage(key, period, usage); err != nil {
			return err
		}
		return tx.PutReservation(result)
	})
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}

// Commit consumes an existing hold without checking a later, possibly reduced
// limit. It moves held units to committed units, so it never increases total
// usage. Monthly usage remains in the reservation's original UTC month.
func (s *QuotaService) Commit(ctx context.Context, org, id string) (Reservation, error) {
	return s.settle(ctx, org, id, Committed)
}

// Release cancels a hold or frees committed reusable capacity. Committed monthly
// consumption cannot be refunded with Release. All valid retries are idempotent.
// Verification concurrency is held from Reserve through Release (Commit optional).
func (s *QuotaService) Release(ctx context.Context, org, id string) (Reservation, error) {
	return s.settle(ctx, org, id, Released)
}

func (s *QuotaService) settle(ctx context.Context, org, id string, target ReservationState) (Reservation, error) {
	if !domain.ValidID(id) {
		return Reservation{}, ErrInvalidQuota
	}
	var result Reservation
	err := s.transaction(ctx, org, func(tx QuotaTransaction) error {
		r, err := tx.Find(id)
		if err != nil {
			return err
		}
		if err := validateReservation(r, org, id); err != nil {
			return err
		}
		if r.State == target {
			result = r
			return nil
		}
		kind, _ := r.Key.Accounting()
		if r.State == Released || (r.State == Committed && kind == domain.Monthly) {
			return ErrQuotaConflict
		}
		u, err := tx.Usage(r.Key, r.Period)
		if err != nil {
			return err
		}
		if !validUsage(u) {
			return ErrQuotaData
		}
		if r.State == Reserved {
			if u.Reserved < r.Amount {
				return ErrQuotaData
			}
			u.Reserved -= r.Amount
		} else {
			if u.Committed < r.Amount {
				return ErrQuotaData
			}
			u.Committed -= r.Amount
		}
		if target == Committed {
			u.Committed += r.Amount
		}
		r.State = target
		if err := tx.PutUsage(r.Key, r.Period, u); err != nil {
			return err
		}
		if err := tx.PutReservation(r); err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return Reservation{}, err
	}
	return result, nil
}

func admit(tx QuotaTransaction, key domain.LimitKey, period string, amount int64) (Usage, error) {
	l, err := tx.Limit(key)
	if err != nil {
		return Usage{}, err
	}
	if l.Key != key || l.Validate() != nil {
		return Usage{}, ErrQuotaData
	}
	if l.State == domain.Disabled {
		return Usage{}, ErrQuotaDisabled
	}
	kind, _ := key.Accounting()
	var u Usage
	if kind != domain.Ceiling {
		u, err = tx.Usage(key, period)
		if err != nil {
			return Usage{}, err
		}
		if !validUsage(u) {
			return Usage{}, ErrQuotaData
		}
	}
	total := u.Reserved + u.Committed
	if amount > math.MaxInt64-total {
		return Usage{}, ErrQuotaBound
	}
	if l.State == domain.Integer && (total > l.Value || amount > l.Value-total) {
		return Usage{}, ErrQuotaExceeded
	}
	return u, nil
}

func validUsage(u Usage) bool {
	return u.Reserved >= 0 && u.Committed >= 0 && u.Reserved <= math.MaxInt64-u.Committed
}

func (s *QuotaService) period(key domain.LimitKey) (string, error) {
	at := s.now().UTC() // Read after acquiring storage lock, including month rollover.
	if at.IsZero() || at.Year() < 1 || at.Year() > 9999 {
		return "", ErrInvalidQuota
	}
	kind, _ := key.Accounting()
	if kind == domain.Monthly {
		return at.Format("2006-01"), nil
	}
	return "lifetime", nil
}

func validateReservation(r Reservation, org, id string) error {
	kind, err := r.Key.Accounting()
	if err != nil || kind == domain.Ceiling || r.OrganizationID != org || r.ID != id || r.Amount <= 0 {
		return ErrQuotaData
	}
	if r.State != Reserved && r.State != Committed && r.State != Released {
		return ErrQuotaData
	}
	if kind == domain.Monthly {
		at, err := time.Parse("2006-01", r.Period)
		if err != nil || at.Year() < 1 || at.Format("2006-01") != r.Period {
			return ErrQuotaData
		}
	} else if r.Period != "lifetime" {
		return ErrQuotaData
	}
	return nil
}
