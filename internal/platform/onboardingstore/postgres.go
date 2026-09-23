package onboardingstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"safepatch/internal/domain/edition"
	flow "safepatch/internal/domain/onboarding"
	org "safepatch/internal/domain/organization"
	"time"
)

type Postgres struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB, now func() time.Time) (*Postgres, error) {
	if db == nil || now == nil {
		return nil, flow.ErrInvalid
	}
	return &Postgres{db, now}, nil
}

var _ flow.Store = (*Postgres)(nil)

func hashID(prefix, value string) string {
	return fmt.Sprintf("%s_%x", prefix, sha256.Sum256([]byte(value)))
}
func decode(raw, organization string) (flow.State, error) {
	var v flow.State
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &v) != nil || v.Validate() != nil || v.OrganizationID != organization {
		return flow.State{}, flow.ErrInvalid
	}
	return v, nil
}
func (s *Postgres) Create(ctx context.Context, actor, attempt, name string) (flow.State, error) {
	if !org.ValidID(actor) || !org.ValidID(attempt) {
		return flow.State{}, flow.ErrInvalid
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	id := hashID("org", actor+":"+attempt)
	o := org.Organization{OrganizationID: id, Name: name, State: org.Active, CreatedAt: now}
	if o.Validate() != nil {
		return flow.State{}, flow.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return flow.State{}, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM users WHERE user_id=$1 FOR UPDATE`, actor).Scan(&active); err != nil || !active {
		return flow.State{}, flow.ErrInvalid
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT canonical_state FROM onboarding_states WHERE creator_user_id=$1 AND creation_attempt=$2`, actor, attempt).Scan(&raw)
	if err == nil {
		v, e := decode(raw, id)
		if e != nil {
			return flow.State{}, e
		}
		if err = tx.Commit(); err != nil {
			return flow.State{}, err
		}
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return flow.State{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM onboarding_states WHERE creator_user_id=$1`, actor).Scan(&count); err != nil {
		return flow.State{}, err
	}
	if count >= 20 {
		return flow.State{}, flow.ErrNotReady
	}
	v := flow.State{OrganizationID: id, Revision: 1, Repositories: []flow.Repository{}}
	record, _ := json.Marshal(v)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO organizations(organization_id,name,state,created_at) VALUES($1,$2,'ACTIVE',$3)`, []any{id, name, now}},
		{`INSERT INTO organization_members(organization_id,user_id,role,active,created_at) VALUES($1,$2,'owner',true,$3)`, []any{id, actor, now}},
		{`INSERT INTO projects(organization_id,project_id,name,active,created_at) VALUES($1,'onboarding_project','First project',true,$2)`, []any{id, now}},
		{`INSERT INTO quota_accounts(organization_id) VALUES($1)`, []any{id}},
		{`INSERT INTO onboarding_states(organization_id,creator_user_id,creation_attempt,revision,canonical_state) VALUES($1,$2,$3,1,$4)`, []any{id, actor, attempt, string(record)}},
	} {
		if _, err = tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return flow.State{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return flow.State{}, err
	}
	return v, nil
}
func (s *Postgres) Load(ctx context.Context, id string) (flow.State, error) {
	if !org.ValidID(id) {
		return flow.State{}, flow.ErrInvalid
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT canonical_state FROM onboarding_states WHERE organization_id=$1`, id).Scan(&raw); err != nil {
		return flow.State{}, err
	}
	return decode(raw, id)
}
func (s *Postgres) Save(ctx context.Context, v flow.State, expected int64) (flow.State, error) {
	if v.Validate() != nil || v.Revision != expected || expected == int64(^uint64(0)>>1) {
		return flow.State{}, flow.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return flow.State{}, err
	}
	defer tx.Rollback()
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM onboarding_states WHERE organization_id=$1 FOR UPDATE`, v.OrganizationID).Scan(&revision); err != nil {
		return flow.State{}, err
	}
	if revision != expected {
		return flow.State{}, flow.ErrConflict
	}
	if v.InstallationID > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_github_connections(organization_id,installation_id) VALUES($1,$2) ON CONFLICT(organization_id) DO NOTHING`, v.OrganizationID, v.InstallationID)
		if err != nil {
			return flow.State{}, err
		}
		var installation int64
		if err = tx.QueryRowContext(ctx, `SELECT installation_id FROM onboarding_github_connections WHERE organization_id=$1`, v.OrganizationID).Scan(&installation); err != nil || installation != v.InstallationID {
			return flow.State{}, flow.ErrConflict
		}
	}
	for _, r := range v.Repositories {
		_, err = tx.ExecContext(ctx, `INSERT INTO repositories(organization_id,project_id,repository_id,name,active,created_at) VALUES($1,'onboarding_project',$2,$3,true,$4) ON CONFLICT(organization_id,repository_id) DO NOTHING`, v.OrganizationID, r.ID, r.FullName, s.now().UTC())
		if err != nil {
			return flow.State{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO onboarding_github_repositories(organization_id,repository_id,installation_id,full_name) VALUES($1,$2,$3,$4) ON CONFLICT(organization_id,repository_id) DO NOTHING`, v.OrganizationID, r.ID, v.InstallationID, r.FullName)
		if err != nil {
			return flow.State{}, err
		}
	}
	v.Revision++
	raw, _ := json.Marshal(v)
	_, err = tx.ExecContext(ctx, `UPDATE onboarding_states SET revision=$2,requested_edition_version_id=NULLIF($3,''),canonical_state=$4 WHERE organization_id=$1`, v.OrganizationID, v.Revision, v.EditionVersionID, string(raw))
	if err != nil {
		return flow.State{}, err
	}
	if err = tx.Commit(); err != nil {
		return flow.State{}, err
	}
	return v, nil
}
func (s *Postgres) Access(ctx context.Context, id, version string) (flow.Access, error) {
	if !org.ValidID(id) || !org.ValidID(version) {
		return flow.Access{}, flow.ErrInvalid
	}
	// Requested edition only selects the comparison target. Read authoritative
	// state independently; never promote a checkout intent to paid state.
	var key string
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT e.edition_key,o.state='ACTIVE' FROM edition_versions e CROSS JOIN organizations o WHERE e.edition_version_id=$1 AND e.sealed AND o.organization_id=$2`, version, id).Scan(&key, &active)
	if err != nil {
		return flow.Access{}, err
	}
	if !active {
		return flow.Access{}, flow.ErrNotReady
	}
	if edition.Key(key) == edition.Community {
		return flow.Access{Ready: true, Source: "COMMUNITY_DEFAULT"}, nil
	}
	var source string
	var until time.Time
	err = s.db.QueryRowContext(ctx, `SELECT source,access_until FROM (
 SELECT 'SUBSCRIPTION' AS source,b.access_until,1 AS priority FROM subscriptions s JOIN billing_subscription_state b ON b.organization_id=s.organization_id AND b.subscription_id=s.subscription_id AND b.revision=s.revision
 WHERE s.organization_id=$1 AND s.edition_version_id=$2 AND s.state IN ('ACTIVE','TRIALING') AND (b.payment_settled OR s.state='TRIALING') AND b.access_until>$3
 UNION ALL SELECT 'TRIAL',expires_at,2 FROM organization_trials WHERE organization_id=$1 AND edition_version_id=$2 AND starts_at<=$3 AND expires_at>$3
 ) sources ORDER BY priority LIMIT 1`, id, version, s.now().UTC()).Scan(&source, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return flow.Access{}, nil
	}
	if err != nil {
		return flow.Access{}, err
	}
	return flow.Access{Ready: true, Source: source, ExpiresAt: &until}, nil
}
