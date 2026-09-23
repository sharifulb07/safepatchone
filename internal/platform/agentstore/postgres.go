package agentstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"

	"safepatch/internal/agent"
	"safepatch/internal/evidence"
)

type Postgres struct{ db *sql.DB }

func NewPostgres(db *sql.DB) (*Postgres, error) {
	if db == nil {
		return nil, agent.ErrInvalid
	}
	return &Postgres{db}, nil
}
func NewID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", agent.ErrUnavailable
	}
	return "lease_" + hex.EncodeToString(b[:]), nil
}
func (s *Postgres) Create(ctx context.Context, l agent.SafeLease) error {
	if s == nil || !validContext(ctx, l.Scope, l.ID) || l.Validate() != nil || l.Uses != 0 || l.Revoked {
		return agent.ErrInvalid
	}
	raw, _ := l.Capabilities.MarshalJSON()
	f, _ := l.Capabilities.Fields()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.ErrUnavailable
	}
	defer tx.Rollback()
	// Serialize per organization to bound active lease records across replicas.
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state='ACTIVE' FROM organizations WHERE organization_id=$1 FOR UPDATE`, l.Scope.OrganizationID).Scan(&active); err != nil || !active {
		return agent.ErrDenied
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM agent_safe_leases WHERE organization_id=$1 AND NOT revoked AND expires_at>clock_timestamp()`, l.Scope.OrganizationID).Scan(&count); err != nil || count >= 4096 {
		return agent.ErrDenied
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_safe_leases(organization_id,repository_id,lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,max_calls,uses,revoked) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$7,$9,0,false)`, l.Scope.OrganizationID, l.Scope.RepositoryID, l.ID, l.Actor.ID, string(l.Actor.Kind), string(raw), l.IssuedAt, l.ExpiresAt, f.CallBudget)
	if err != nil {
		return agent.ErrUnavailable
	}
	return tx.Commit()
}

const leaseColumns = `lease_id,actor_id,actor_kind,capabilities,issued_at,expires_at,last_used_at,uses,revoked`

type scanner interface{ Scan(...any) error }

func scanLease(row scanner, scope agent.Scope) (agent.SafeLease, error) {
	l := agent.SafeLease{Scope: scope}
	var raw string
	if err := row.Scan(&l.ID, &l.Actor.ID, &l.Actor.Kind, &raw, &l.IssuedAt, &l.ExpiresAt, &l.LastUsedAt, &l.Uses, &l.Revoked); err != nil {
		return l, agent.ErrUnavailable
	}
	c, err := agent.DecodeCapabilities([]byte(raw))
	if err != nil {
		return l, agent.ErrUnavailable
	}
	l.Capabilities = c
	if l.Validate() != nil {
		return l, agent.ErrUnavailable
	}
	return l, nil
}
func (s *Postgres) Load(ctx context.Context, scope agent.Scope, id string) (agent.SafeLease, error) {
	if s == nil || !validContext(ctx, scope, id) {
		return agent.SafeLease{}, agent.ErrInvalid
	}
	return scanLease(s.db.QueryRowContext(ctx, `SELECT `+leaseColumns+` FROM agent_safe_leases WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3`, scope.OrganizationID, scope.RepositoryID, id), scope)
}
func (s *Postgres) Consume(ctx context.Context, scope agent.Scope, id, invocation, hash string, apply func(*agent.SafeLease) error) error {
	if s == nil || !validContext(ctx, scope, id) || !evidence.ValidIdentifier(invocation) || (evidence.OriginalReference{SHA256: hash}).Validate() != nil || apply == nil {
		return agent.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return agent.ErrUnavailable
	}
	defer tx.Rollback()
	old, err := scanLease(tx.QueryRowContext(ctx, `SELECT `+leaseColumns+` FROM agent_safe_leases WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3 FOR UPDATE`, scope.OrganizationID, scope.RepositoryID, id), scope)
	if err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_invocations WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3 AND invocation_id=$4)`, scope.OrganizationID, scope.RepositoryID, id, invocation).Scan(&exists); err != nil {
		return agent.ErrUnavailable
	}
	if exists {
		return agent.ErrReplay
	}
	next := old
	if err = apply(&next); err != nil {
		return err
	}
	if !validAdvance(old, next) {
		return agent.ErrDenied
	}
	_, err = tx.ExecContext(ctx, `UPDATE agent_safe_leases SET uses=$4,last_used_at=$5 WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3`, scope.OrganizationID, scope.RepositoryID, id, next.Uses, next.LastUsedAt)
	if err != nil {
		return agent.ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_invocations(organization_id,repository_id,lease_id,invocation_id,call_digest,outcome,result_digest,finished) VALUES($1,$2,$3,$4,$5,'UNKNOWN','',false)`, scope.OrganizationID, scope.RepositoryID, id, invocation, hash)
	if err != nil {
		return agent.ErrUnavailable
	}
	return tx.Commit()
}
func (s *Postgres) Finish(ctx context.Context, scope agent.Scope, id, invocation string, r agent.Receipt) error {
	if s == nil || !validContext(ctx, scope, id) || !evidence.ValidIdentifier(invocation) || !r.Outcome.Valid() || r.Outcome == agent.OutcomeUnknown || (evidence.OriginalReference{SHA256: r.ResultDigest}).Validate() != nil {
		return agent.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_invocations SET outcome=$5,result_digest=$6,finished=true WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3 AND invocation_id=$4 AND NOT finished`, scope.OrganizationID, scope.RepositoryID, id, invocation, string(r.Outcome), r.ResultDigest)
	if err != nil {
		return agent.ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return agent.ErrDenied
	}
	return nil
}
func (s *Postgres) Revoke(ctx context.Context, scope agent.Scope, id string) error {
	if s == nil || !validContext(ctx, scope, id) {
		return agent.ErrInvalid
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_safe_leases SET revoked=true WHERE organization_id=$1 AND repository_id=$2 AND lease_id=$3`, scope.OrganizationID, scope.RepositoryID, id)
	if err != nil {
		return agent.ErrUnavailable
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return agent.ErrDenied
	}
	return nil
}
