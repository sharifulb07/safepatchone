package githubapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/trust"
	"time"
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func NewStore(db *sql.DB, now func() time.Time) (*Store, error) {
	if db == nil || now == nil {
		return nil, application.ErrChangeWorkflow
	}
	return &Store{db, now}, nil
}
func (s *Store) Resolve(ctx context.Context, p application.PullRequest) (application.PullRequestBinding, error) {
	var b application.PullRequestBinding
	b.Actor.Kind = authorization.User
	if p.Validate() != nil {
		return b, application.ErrChangeWorkflow
	}
	err := s.db.QueryRowContext(ctx, `SELECT b.organization_id,b.repository_id,b.actor_user_id,b.policy_hash FROM github_pr_bindings b JOIN onboarding_github_repositories g ON g.organization_id=b.organization_id AND g.repository_id=b.repository_id JOIN onboarding_github_connections c ON c.organization_id=g.organization_id AND c.installation_id=g.installation_id WHERE g.installation_id=$1 AND g.repository_id=$2 AND b.enabled`, p.InstallationID, "gh_"+formatID(p.RepositoryID)).Scan(&b.OrganizationID, &b.RepositoryID, &b.Actor.ID, &b.PolicyHash)
	if err != nil {
		return b, application.ErrChangeWorkflow
	}
	return b, nil
}
func (s *Store) Accept(ctx context.Context, delivery, hash string, j application.PullRequestJob) error {
	raw, err := json.Marshal(j)
	if err != nil || len(raw) > 8192 || j.Request.Validate() != nil || j.Change().Validate() != nil {
		return application.ErrChangeWorkflow
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize admission/counting by organization to bound unprocessed work.
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT state='ACTIVE' FROM organizations WHERE organization_id=$1 FOR UPDATE`, j.Binding.OrganizationID).Scan(&active); err != nil || !active {
		return application.ErrChangeWorkflow
	}
	var oldHash, oldJob string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash,job_id FROM github_pr_deliveries WHERE delivery_id=$1`, delivery).Scan(&oldHash, &oldJob)
	if err == nil {
		if oldHash != hash || oldJob != j.ID {
			return application.ErrChangeWorkflow
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM github_pr_deliveries d JOIN github_pr_jobs j ON j.job_id=d.job_id WHERE j.organization_id=$1 AND d.received_at>$2`, j.Binding.OrganizationID, s.now().Add(-time.Hour)).Scan(&count); err != nil {
		return err
	}
	if count >= 1000 {
		return application.ErrChangeWorkflow
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM github_pr_jobs WHERE organization_id=$1 AND status<>'DONE'`, j.Binding.OrganizationID).Scan(&count); err != nil {
		return err
	}
	if count >= 100 {
		return application.ErrChangeWorkflow
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO github_pr_jobs(job_id,organization_id,repository_id,canonical_job,status) VALUES($1,$2,$3,$4,'PENDING') ON CONFLICT(job_id) DO NOTHING`, j.ID, j.Binding.OrganizationID, j.Binding.RepositoryID, string(raw)); err != nil {
		return err
	}
	var stored string
	if err = tx.QueryRowContext(ctx, `SELECT canonical_job FROM github_pr_jobs WHERE job_id=$1 AND organization_id=$2`, j.ID, j.Binding.OrganizationID).Scan(&stored); err != nil || stored != string(raw) {
		return application.ErrChangeWorkflow
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO github_pr_deliveries(delivery_id,payload_hash,job_id,received_at) VALUES($1,$2,$3,$4)`, delivery, hash, j.ID, s.now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}
func decodeJob(raw, id string) (application.PullRequestJob, error) {
	var j application.PullRequestJob
	checked, err := jsonobject.Normalize([]byte(raw), 8192)
	if err != nil || json.Unmarshal(checked, &j) != nil || j.ID != id || j.Request.Validate() != nil || j.Change().Validate() != nil || j.Binding.Actor.Validate() != nil {
		return j, application.ErrChangeWorkflow
	}
	return j, nil
}
func (s *Store) claim(ctx context.Context, org, id, lease string) (application.PullRequestJob, bool, error) {
	if !evidence.ValidIdentifier(org) || !evidence.ValidIdentifier(id) || !evidence.ValidIdentifier(lease) {
		return application.PullRequestJob{}, false, application.ErrChangeWorkflow
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `UPDATE github_pr_jobs SET status='RUNNING',lease_id=$2,lease_until=$3 WHERE job_id=$1 AND organization_id=$5 AND (status='PENDING' OR (status='RUNNING' AND lease_until<$4)) RETURNING canonical_job`, id, lease, s.now().Add(10*time.Minute), s.now(), org).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return application.PullRequestJob{}, false, nil
	}
	if err != nil {
		return application.PullRequestJob{}, false, err
	}
	j, err := decodeJob(raw, id)
	if err == nil && j.Binding.OrganizationID != org {
		return application.PullRequestJob{}, false, application.ErrChangeWorkflow
	}
	return j, true, err
}
func (s *Store) finish(ctx context.Context, j application.PullRequestJob, lease string, r application.ChangeResult) error {
	if r.ChangeID != j.Change().ID() {
		return application.ErrChangeWorkflow
	}
	switch r.Decision {
	case trust.Pass, trust.Fail:
		if r.EvidenceReference == "" {
			return application.ErrChangeWorkflow
		}
	case trust.Unknown:
	default:
		return application.ErrChangeWorkflow
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 8192 {
		return application.ErrChangeWorkflow
	}
	done, err := s.db.ExecContext(ctx, `UPDATE github_pr_jobs SET status='READY',canonical_result=$3,lease_id=NULL,lease_until=NULL WHERE job_id=$1 AND organization_id=$4 AND lease_id=$2 AND status='RUNNING'`, j.ID, lease, string(raw), j.Binding.OrganizationID)
	if err != nil {
		return err
	}
	n, err := done.RowsAffected()
	if err != nil || n != 1 {
		return application.ErrChangeWorkflow
	}
	return nil
}

// Check delivery is a separate durable phase. Holding this row lock serializes
// publishers. Verification is never rerun just because GitHub is unavailable.
func (s *Store) publishResult(ctx context.Context, org, id string, publish func(context.Context, application.PullRequestJob, application.ChangeResult) error) error {
	if ctx == nil || !evidence.ValidIdentifier(org) || !evidence.ValidIdentifier(id) || publish == nil {
		return application.ErrChangeWorkflow
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw, result string
	err = tx.QueryRowContext(ctx, `SELECT canonical_job,canonical_result FROM github_pr_jobs WHERE job_id=$1 AND organization_id=$2 AND status='READY' FOR UPDATE`, id, org).Scan(&raw, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	j, err := decodeJob(raw, id)
	if err == nil && j.Binding.OrganizationID != org {
		return application.ErrChangeWorkflow
	}
	if err != nil {
		return err
	}
	var r application.ChangeResult
	if len(result) > 8192 || json.Unmarshal([]byte(result), &r) != nil || r.ChangeID != j.Change().ID() {
		return application.ErrChangeWorkflow
	}
	if err = publish(ctx, j, r); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE github_pr_jobs SET status='DONE' WHERE job_id=$1 AND organization_id=$2 AND status='READY'`, id, org); err != nil {
		return err
	}
	return tx.Commit()
}
