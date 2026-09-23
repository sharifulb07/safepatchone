package onboardingstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/organization"
	"time"
)

// Publisher must await a durable JetStream acknowledgement. Publish errors leave
// the transactional outbox pending. Retrying uses a stable message identity.
type Publisher interface {
	Publish(context.Context, string, string) error
}
type Proofs struct {
	store     *Postgres
	publisher Publisher
}

func NewProofs(s *Postgres, p Publisher) (*Proofs, error) {
	if s == nil || p == nil {
		return nil, flow.ErrInvalid
	}
	return &Proofs{s, p}, nil
}
func (p *Proofs) Submit(ctx context.Context, actor authorization.Principal, v flow.State, attempt string) (application.FirstProof, error) {
	if v.Validate() != nil || actor.Validate() != nil || !organization.ValidID(attempt) || len(v.Repositories) == 0 || v.PolicyID == "" {
		return application.FirstProof{}, flow.ErrInvalid
	}
	id := hashID("proof", attempt)
	tx, err := p.store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.FirstProof{}, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT canonical_state FROM onboarding_states WHERE organization_id=$1 FOR UPDATE`, v.OrganizationID).Scan(&raw); err != nil {
		return application.FirstProof{}, err
	}
	current, err := decode(raw, v.OrganizationID)
	if err != nil {
		return application.FirstProof{}, err
	}
	if current.ProofID != "" {
		if err = tx.Commit(); err != nil {
			return application.FirstProof{}, err
		}
		return p.Read(ctx, v.OrganizationID, current.ProofID)
	}
	if current.Revision != v.Revision {
		return application.FirstProof{}, flow.ErrConflict
	}
	request, _ := json.Marshal(v)
	_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_proof_jobs(organization_id,proof_id,actor_user_id,onboarding_revision,canonical_request,status) VALUES($1,$2,$3,$4,$5,'PENDING')`, v.OrganizationID, id, actor.ID, v.Revision, string(request))
	if err != nil {
		return application.FirstProof{}, err
	}
	current.ProofID = id
	current.Revision++
	record, _ := json.Marshal(current)
	_, err = tx.ExecContext(ctx, `UPDATE onboarding_states SET revision=$2,canonical_state=$3 WHERE organization_id=$1`, v.OrganizationID, current.Revision, string(record))
	if err != nil {
		return application.FirstProof{}, err
	}
	if err = tx.Commit(); err != nil {
		return application.FirstProof{}, err
	}
	// Durable intent is visible even when the broker is temporarily unavailable.
	_ = p.publish(ctx, v.OrganizationID, id)
	return p.Read(ctx, v.OrganizationID, id)
}
func (p *Proofs) Read(ctx context.Context, org, id string) (application.FirstProof, error) {
	if !organization.ValidID(org) || !organization.ValidID(id) {
		return application.FirstProof{}, flow.ErrInvalid
	}
	v := application.FirstProof{ID: id}
	err := p.store.db.QueryRowContext(ctx, `SELECT status FROM onboarding_proof_jobs WHERE organization_id=$1 AND proof_id=$2`, org, id).Scan(&v.Status)
	if err != nil {
		return application.FirstProof{}, err
	}
	switch v.Status {
	case "PENDING", "QUEUED", "RUNNING", "PASS", "FAIL", "UNKNOWN", "ERROR":
	default:
		return application.FirstProof{}, flow.ErrInvalid
	}
	return v, nil
}
func (p *Proofs) publish(ctx context.Context, org, id string) error {
	if err := p.publisher.Publish(ctx, org, id); err != nil {
		return err
	}
	_, err := p.store.db.ExecContext(ctx, `UPDATE onboarding_proof_jobs SET published_at=$3,status=CASE WHEN status='PENDING' THEN 'QUEUED' ELSE status END WHERE organization_id=$1 AND proof_id=$2 AND published_at IS NULL`, org, id, p.store.now().UTC())
	return err
}
func (p *Proofs) Flush(ctx context.Context) error {
	rows, err := p.store.db.QueryContext(ctx, `SELECT organization_id,proof_id FROM onboarding_proof_jobs WHERE published_at IS NULL ORDER BY organization_id,proof_id LIMIT 100`)
	if err != nil {
		return err
	}
	type job struct{ org, id string }
	jobs := []job{}
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.org, &j.id); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if err = p.publish(ctx, j.org, j.id); err != nil {
			return err
		}
	}
	return nil
}

// Executor is a trusted worker adapter around existing verifier/runner/evidence
// services. It must recheck current tenant/feature/permission/quota admission for
// EVERY selected repository, pin immutable revisions and persist immutable proof
// evidence before returning its reference. Never bind this port to HTTP input.
type Executor interface {
	Execute(context.Context, authorization.Principal, flow.State) (status, evidenceReference string, err error)
}

// Run claims a fenced lease before invoking the trusted executor. Queue messages
// supply only IDs. A duplicate or expired worker cannot overwrite a newer result.
func (p *Proofs) Run(ctx context.Context, org, id, lease string, executor *GuardedExecutor) error {
	if !organization.ValidID(org) || !organization.ValidID(id) || !organization.ValidID(lease) || executor == nil {
		return flow.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var raw, user string
	err := p.store.db.QueryRowContext(ctx, `UPDATE onboarding_proof_jobs SET status='RUNNING',lease_id=$3,lease_until=$4 WHERE organization_id=$1 AND proof_id=$2 AND (status IN ('PENDING','QUEUED') OR (status='RUNNING' AND lease_until<$5)) RETURNING canonical_request,actor_user_id`, org, id, lease, p.store.now().Add(6*time.Minute), p.store.now()).Scan(&raw, &user)
	if errors.Is(err, sql.ErrNoRows) {
		v, e := p.Read(ctx, org, id)
		if e != nil {
			return e
		}
		if v.Status == "RUNNING" {
			return flow.ErrConflict
		}
		return nil
	}
	if err != nil {
		return err
	}
	v, err := decode(raw, org)
	status, reference := "UNKNOWN", ""
	if err == nil {
		status, reference, err = executor.Execute(ctx, authorization.Principal{ID: user, Kind: authorization.User}, v)
	}
	if err != nil || ctx.Err() != nil {
		status = "UNKNOWN"
		reference = ""
	}
	switch status {
	case "PASS", "FAIL":
		if reference == "" {
			status = "UNKNOWN"
		}
	case "UNKNOWN", "ERROR":
	default:
		status = "UNKNOWN"
	}
	if len(reference) > 256 {
		status = "UNKNOWN"
		reference = ""
	}
	// Use an independent bounded context to record timeout as UNKNOWN.
	finish, c := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer c()
	result, e := p.store.db.ExecContext(finish, `UPDATE onboarding_proof_jobs SET status=$4,evidence_reference=NULLIF($5,''),lease_id=NULL,lease_until=NULL WHERE organization_id=$1 AND proof_id=$2 AND lease_id=$3 AND status='RUNNING'`, org, id, lease, status, reference)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return flow.ErrConflict
	}
	return nil
}
