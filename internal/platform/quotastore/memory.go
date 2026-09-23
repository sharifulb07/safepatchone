// Package quotastore supplies transactional quota storage adapters.
package quotastore

import (
	"context"
	"maps"

	domain "safepatch/internal/domain/entitlement"
	"safepatch/internal/entitlement"
)

type bucket struct {
	key    domain.LimitKey
	period string
}
type memoryAccount struct {
	limits       map[domain.LimitKey]domain.EntitlementLimit
	usage        map[bucket]entitlement.Usage
	reservations map[string]entitlement.Reservation
}

// Memory is a bounded, process-local adapter for tests and embedded use. It is
// NOT durable or suitable for quotas shared by multiple worker processes.
// Full ledgers fail closed; no eviction can silently permit duplicate charges.
type Memory struct {
	lock                              chan struct{}
	accounts                          map[string]*memoryAccount
	maxOrganizations, maxReservations int
}

func NewMemory(maxOrganizations, maxReservationsPerOrganization int) (*Memory, error) {
	if maxOrganizations <= 0 || maxReservationsPerOrganization <= 0 {
		return nil, entitlement.ErrInvalidQuota
	}
	return &Memory{lock: make(chan struct{}, 1), accounts: make(map[string]*memoryAccount), maxOrganizations: maxOrganizations, maxReservations: maxReservationsPerOrganization}, nil
}

var _ entitlement.QuotaStore = (*Memory)(nil)

func (s *Memory) WithinOrganization(ctx context.Context, org string, fn func(entitlement.QuotaTransaction) error) error {
	if s == nil || s.lock == nil || s.accounts == nil || s.maxOrganizations <= 0 || s.maxReservations <= 0 || ctx == nil || !domain.ValidID(org) || fn == nil {
		return entitlement.ErrInvalidQuota
	}
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.lock }()
	if err := ctx.Err(); err != nil {
		return err
	}
	account, exists := s.accounts[org]
	if !exists {
		if len(s.accounts) >= s.maxOrganizations {
			return entitlement.ErrQuotaBound
		}
		account = &memoryAccount{limits: map[domain.LimitKey]domain.EntitlementLimit{}, usage: map[bucket]entitlement.Usage{}, reservations: map[string]entitlement.Reservation{}}
	}
	copy := &memoryAccount{limits: maps.Clone(account.limits), usage: maps.Clone(account.usage), reservations: maps.Clone(account.reservations)}
	tx := &memoryTx{org: org, account: copy, maxReservations: s.maxReservations}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.accounts[org] = copy
	return nil
}

// SetLimit is a trusted provisioning boundary, not a public entitlement API.
// The caller must resolve effective policy and authorize the policy change.
// It preserves usage and reservation history, including on downgrade/disable.
func (s *Memory) SetLimit(ctx context.Context, org string, limit domain.EntitlementLimit) error {
	if err := limit.Validate(); err != nil {
		return err
	}
	return s.WithinOrganization(ctx, org, func(tx entitlement.QuotaTransaction) error {
		tx.(*memoryTx).account.limits[limit.Key] = limit
		return nil
	})
}

type memoryTx struct {
	org             string
	account         *memoryAccount
	maxReservations int
}

func (t *memoryTx) Limit(key domain.LimitKey) (domain.EntitlementLimit, error) {
	if l, ok := t.account.limits[key]; ok {
		return l, nil
	}
	return domain.EntitlementLimit{Key: key, State: domain.Disabled}, nil
}
func (t *memoryTx) Usage(key domain.LimitKey, period string) (entitlement.Usage, error) {
	return t.account.usage[bucket{key, period}], nil
}
func (t *memoryTx) Find(id string) (entitlement.Reservation, error) {
	r, ok := t.account.reservations[id]
	if !ok {
		return entitlement.Reservation{}, entitlement.ErrQuotaNotFound
	}
	return r, nil
}
func (t *memoryTx) PutUsage(key domain.LimitKey, period string, u entitlement.Usage) error {
	t.account.usage[bucket{key, period}] = u
	return nil
}
func (t *memoryTx) PutReservation(r entitlement.Reservation) error {
	if r.OrganizationID != t.org {
		return entitlement.ErrQuotaData
	}
	if _, exists := t.account.reservations[r.ID]; !exists && len(t.account.reservations) >= t.maxReservations {
		return entitlement.ErrQuotaBound
	}
	t.account.reservations[r.ID] = r
	return nil
}
