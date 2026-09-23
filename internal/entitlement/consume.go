package entitlement

import (
	"context"
	"errors"
	domain "safepatch/internal/domain/entitlement"
)

// ConsumeOnce atomically claims and charges a monthly attempt. Unlike Reserve,
// duplicate calls never authorize execution again, including concurrent callers.
// A crash after commitment consumes the attempt; retries require a new ID.
func (s *QuotaService) ConsumeOnce(ctx context.Context, org string, key domain.LimitKey, amount int64, id string) (Reservation, error) {
	bad := Reservation{}
	kind, err := key.Accounting()
	if err != nil || kind != domain.Monthly || amount <= 0 || !domain.ValidID(id) {
		return bad, ErrInvalidQuota
	}
	result := bad
	err = s.transaction(ctx, org, func(tx QuotaTransaction) error {
		_, err := tx.Find(id)
		if err == nil {
			return ErrQuotaConflict
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
		usage.Committed += amount
		result = Reservation{OrganizationID: org, ID: id, Key: key, Period: period, Amount: amount, State: Committed}
		if err := tx.PutUsage(key, period, usage); err != nil {
			return err
		}
		return tx.PutReservation(result)
	})
	if err != nil {
		return bad, err
	}
	return result, nil
}
