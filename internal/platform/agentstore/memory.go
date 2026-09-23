// Package agentstore implements shared SafeLease persistence.
package agentstore

import (
	"context"
	"sync"

	"safepatch/internal/agent"
	"safepatch/internal/evidence"
)

// Memory is bounded process-local storage for local development/tests. Hosted
// deployments use Postgres; restarting Memory must not preserve access tokens.
type Memory struct {
	mu     sync.Mutex
	leases map[string]agent.SafeLease
	calls  map[string]callRecord
}
type callRecord struct {
	hash     string
	receipt  agent.Receipt
	finished bool
}

func NewMemory() *Memory {
	return &Memory{leases: map[string]agent.SafeLease{}, calls: map[string]callRecord{}}
}
func leaseKey(s agent.Scope, id string) string {
	return s.OrganizationID + "/" + s.RepositoryID + "/" + id
}
func validContext(ctx context.Context, s agent.Scope, id string) bool {
	return ctx != nil && ctx.Err() == nil && s.Validate() == nil && evidence.ValidIdentifier(id)
}
func validAdvance(old, next agent.SafeLease) bool {
	return next.Validate() == nil && old.ID == next.ID && old.Scope == next.Scope && old.Actor == next.Actor && old.Capabilities.Digest() == next.Capabilities.Digest() && old.IssuedAt.Equal(next.IssuedAt) && old.ExpiresAt.Equal(next.ExpiresAt) && !old.Revoked && !next.Revoked && next.Uses == old.Uses+1 && !next.LastUsedAt.Before(old.LastUsedAt)
}
func (s *Memory) Create(ctx context.Context, l agent.SafeLease) error {
	if s == nil || !validContext(ctx, l.Scope, l.ID) || l.Validate() != nil || l.Uses != 0 || l.Revoked {
		return agent.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := leaseKey(l.Scope, l.ID)
	if _, ok := s.leases[key]; ok || len(s.leases) >= 4096 {
		return agent.ErrDenied
	}
	s.leases[key] = l
	return nil
}
func (s *Memory) Load(ctx context.Context, scope agent.Scope, id string) (agent.SafeLease, error) {
	if s == nil || !validContext(ctx, scope, id) {
		return agent.SafeLease{}, agent.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[leaseKey(scope, id)]
	if !ok {
		return l, agent.ErrDenied
	}
	return l, nil
}
func (s *Memory) Consume(ctx context.Context, scope agent.Scope, id, invocation, hash string, apply func(*agent.SafeLease) error) error {
	if s == nil || !validContext(ctx, scope, id) || !evidence.ValidIdentifier(invocation) || (evidence.OriginalReference{SHA256: hash}).Validate() != nil || apply == nil {
		return agent.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := leaseKey(scope, id)
	callKey := key + "/" + invocation
	if _, ok := s.calls[callKey]; ok {
		return agent.ErrReplay
	}
	old, ok := s.leases[key]
	if !ok {
		return agent.ErrDenied
	}
	next := old
	if err := apply(&next); err != nil {
		return err
	}
	if !validAdvance(old, next) || ctx.Err() != nil {
		return agent.ErrDenied
	}
	s.leases[key] = next
	s.calls[callKey] = callRecord{hash: hash, receipt: agent.Receipt{Outcome: agent.OutcomeUnknown}}
	return nil
}
func (s *Memory) Finish(ctx context.Context, scope agent.Scope, id, invocation string, r agent.Receipt) error {
	if s == nil || !validContext(ctx, scope, id) || !evidence.ValidIdentifier(invocation) || !r.Outcome.Valid() || r.Outcome == agent.OutcomeUnknown || (evidence.OriginalReference{SHA256: r.ResultDigest}).Validate() != nil {
		return agent.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := leaseKey(scope, id) + "/" + invocation
	old, ok := s.calls[key]
	if !ok || old.finished {
		return agent.ErrDenied
	}
	old.receipt = r
	old.finished = true
	s.calls[key] = old
	return nil
}
func (s *Memory) Revoke(ctx context.Context, scope agent.Scope, id string) error {
	if s == nil || !validContext(ctx, scope, id) {
		return agent.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := leaseKey(scope, id)
	l, ok := s.leases[key]
	if !ok {
		return agent.ErrDenied
	}
	l.Revoked = true
	s.leases[key] = l
	return nil
}
