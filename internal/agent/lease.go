package agent

import (
	"context"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/evidence"
)

// SafeLease is a server-owned, revocable record, not a bearer token. A caller
// must authenticate as this exact principal in this scope on every invocation.
type SafeLease struct {
	ID                              string
	Scope                           Scope
	Actor                           authorization.Principal
	Capabilities                    Capabilities
	IssuedAt, ExpiresAt, LastUsedAt time.Time
	Uses                            uint32
	Revoked                         bool
}

func NewSafeLease(id string, actor authorization.Principal, c Capabilities, now time.Time) (SafeLease, error) {
	f, err := c.Fields()
	if err != nil {
		return SafeLease{}, ErrInvalid
	}
	now = now.UTC().Truncate(time.Microsecond)
	l := SafeLease{ID: id, Scope: f.Scope, Actor: actor, Capabilities: c, IssuedAt: now, ExpiresAt: now.Add(time.Duration(f.LeaseSeconds) * time.Second), LastUsedAt: now}
	if l.Validate() != nil {
		return SafeLease{}, ErrInvalid
	}
	return l, nil
}
func validTime(t time.Time) bool {
	return !t.IsZero() && t.UTC().Year() >= 1 && t.UTC().Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func (l SafeLease) Validate() error {
	f, err := l.Capabilities.Fields()
	if err != nil || !evidence.ValidIdentifier(l.ID) || l.Scope.Validate() != nil || l.Scope != f.Scope || l.Actor.Validate() != nil || !validTime(l.IssuedAt) || !validTime(l.ExpiresAt) || !validTime(l.LastUsedAt) || !l.ExpiresAt.Equal(l.IssuedAt.Add(time.Duration(f.LeaseSeconds)*time.Second)) || l.LastUsedAt.Before(l.IssuedAt) || !l.LastUsedAt.Before(l.ExpiresAt) || l.Uses > f.CallBudget {
		return ErrInvalid
	}
	return nil
}

// TemporalGuard evaluates current time and context every time a call is consumed.
// Exact expiry, backwards clocks, policy/inventory drift and exhausted/revoked
// leases fail closed. No silent renewal or grandfathering after a plan change.
type TemporalGuard struct{}

func (TemporalGuard) Check(l SafeLease, actor authorization.Principal, scope Scope, call Call, inventoryDigest, policyDigest string, now time.Time) error {
	if l.Validate() != nil || !validTime(now) || l.Actor != actor || l.Scope != scope || l.Revoked || now.Before(l.IssuedAt) || now.Before(l.LastUsedAt) || !now.Before(l.ExpiresAt) {
		return ErrDenied
	}
	f, _ := l.Capabilities.Fields()
	if f.InventoryDigest != inventoryDigest || f.PolicyDigest != policyDigest || l.Uses >= f.CallBudget {
		return ErrDenied
	}
	for _, g := range f.Grants {
		if g.CallDigest == call.Digest() {
			return nil
		}
	}
	return ErrDenied
}

type Outcome string

const OutcomeUnknown Outcome = "UNKNOWN"
const OutcomeReturned Outcome = "RETURNED"
const OutcomeToolError Outcome = "TOOL_ERROR"

func (o Outcome) Valid() bool {
	return o == OutcomeUnknown || o == OutcomeReturned || o == OutcomeToolError
}

type Receipt struct {
	Outcome      Outcome `json:"outcome"`
	ResultDigest string  `json:"result_digest"`
}

// LeaseStore consumes an invocation atomically before dispatch, durably recording
// UNKNOWN. It never retries a consumed invocation: a crashed/timed-out tool may
// have acted. Finish is immutable; result bodies and arguments are not logged.
type LeaseStore interface {
	Create(context.Context, SafeLease) error
	Load(context.Context, Scope, string) (SafeLease, error)
	Consume(context.Context, Scope, string, string, string, func(*SafeLease) error) error
	Finish(context.Context, Scope, string, string, Receipt) error
	Revoke(context.Context, Scope, string) error
}
