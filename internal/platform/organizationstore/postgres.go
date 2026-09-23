// Package organizationstore persists the shared organization-owned SaaS model.
// Composition owns primary DB connection, credentials and fixed search_path.
// Writers are trusted provisioning/billing services, never client/scanner roles.
package organizationstore

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	org "safepatch/internal/domain/organization"
	"safepatch/internal/tenant"
)

type Postgres struct {
	db      *sql.DB
	catalog *feature.FeatureCatalog
}

func NewPostgres(db *sql.DB, catalog *feature.FeatureCatalog) (*Postgres, error) {
	if db == nil || catalog == nil {
		return nil, org.ErrInvalidEntity
	}
	return &Postgres{db, catalog}, nil
}

var _ org.Store = (*Postgres)(nil)
var _ authorization.MembershipRepository = (*Postgres)(nil)

func (s *Postgres) check(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil {
		return org.ErrInvalidEntity
	}
	return ctx.Err()
}
func scoped(organizationID, id string) bool { return org.ValidID(organizationID) && org.ValidID(id) }
func storageError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return org.ErrNotFound
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() == "23505" {
		return org.ErrConflict
	}
	return err
}
func (s *Postgres) exec(ctx context.Context, query string, args ...any) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, query, args...)
	return storageError(err)
}

func (s *Postgres) CreateUser(ctx context.Context, u org.User) error {
	if err := u.Validate(); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO users (user_id,display_name,active,created_at) VALUES ($1,$2,$3,$4)`, u.UserID, u.DisplayName, u.Active, u.CreatedAt.UTC())
}
func (s *Postgres) GetUser(ctx context.Context, id string) (org.User, error) {
	if err := s.check(ctx); err != nil {
		return org.User{}, err
	}
	if !org.ValidID(id) {
		return org.User{}, org.ErrInvalidEntity
	}
	var u org.User
	err := s.db.QueryRowContext(ctx, `SELECT user_id,display_name,active,created_at FROM users WHERE user_id=$1`, id).Scan(&u.UserID, &u.DisplayName, &u.Active, &u.CreatedAt)
	if err != nil {
		return org.User{}, storageError(err)
	}
	if u.Validate() != nil || u.UserID != id {
		return org.User{}, org.ErrData
	}
	return u, nil
}
func (s *Postgres) CreateOrganization(ctx context.Context, o org.Organization) error {
	if err := o.Validate(); err != nil {
		return err
	}
	return s.exec(ctx, `INSERT INTO organizations (organization_id,name,state,created_at) VALUES ($1,$2,$3,$4)`, o.OrganizationID, o.Name, string(o.State), o.CreatedAt.UTC())
}
func (s *Postgres) GetOrganization(ctx context.Context, id string) (org.Organization, error) {
	if err := s.check(ctx); err != nil {
		return org.Organization{}, err
	}
	if !org.ValidID(id) {
		return org.Organization{}, org.ErrInvalidEntity
	}
	var o org.Organization
	err := s.db.QueryRowContext(ctx, `SELECT organization_id,name,state,created_at FROM organizations WHERE organization_id=$1`, id).Scan(&o.OrganizationID, &o.Name, &o.State, &o.CreatedAt)
	if err != nil {
		return org.Organization{}, storageError(err)
	}
	if o.Validate() != nil || o.OrganizationID != id {
		return org.Organization{}, org.ErrData
	}
	return o, nil
}
func (s *Postgres) CreateProject(ctx context.Context, organizationID string, p org.Project) error {
	if p.Validate() != nil || p.OrganizationID != organizationID {
		return org.ErrInvalidEntity
	}
	return s.exec(ctx, `INSERT INTO projects (organization_id,project_id,name,active,created_at) VALUES ($1,$2,$3,$4,$5)`, organizationID, p.ProjectID, p.Name, p.Active, p.CreatedAt.UTC())
}
func (s *Postgres) GetProject(ctx context.Context, organizationID, id string) (org.Project, error) {
	if err := s.check(ctx); err != nil {
		return org.Project{}, err
	}
	if !scoped(organizationID, id) {
		return org.Project{}, org.ErrInvalidEntity
	}
	var p org.Project
	err := s.db.QueryRowContext(ctx, `SELECT organization_id,project_id,name,active,created_at FROM projects WHERE organization_id=$1 AND project_id=$2`, organizationID, id).Scan(&p.OrganizationID, &p.ProjectID, &p.Name, &p.Active, &p.CreatedAt)
	if err != nil {
		return org.Project{}, storageError(err)
	}
	if p.Validate() != nil || p.OrganizationID != organizationID || p.ProjectID != id {
		return org.Project{}, org.ErrData
	}
	return p, nil
}
func (s *Postgres) CreateRepository(ctx context.Context, organizationID string, r org.Repository) error {
	if r.Validate() != nil || r.OrganizationID != organizationID {
		return org.ErrInvalidEntity
	}
	return s.exec(ctx, `INSERT INTO repositories (organization_id,project_id,repository_id,name,active,created_at) VALUES ($1,$2,$3,$4,$5,$6)`, organizationID, r.ProjectID, r.RepositoryID, r.Name, r.Active, r.CreatedAt.UTC())
}
func (s *Postgres) GetRepository(ctx context.Context, organizationID, id string) (org.Repository, error) {
	if err := s.check(ctx); err != nil {
		return org.Repository{}, err
	}
	if !scoped(organizationID, id) {
		return org.Repository{}, org.ErrInvalidEntity
	}
	var r org.Repository
	err := s.db.QueryRowContext(ctx, `SELECT organization_id,project_id,repository_id,name,active,created_at FROM repositories WHERE organization_id=$1 AND repository_id=$2`, organizationID, id).Scan(&r.OrganizationID, &r.ProjectID, &r.RepositoryID, &r.Name, &r.Active, &r.CreatedAt)
	if err != nil {
		return org.Repository{}, storageError(err)
	}
	if r.Validate() != nil || r.OrganizationID != organizationID || r.RepositoryID != id {
		return org.Repository{}, org.ErrData
	}
	return r, nil
}
func validMember(m authorization.Membership) bool {
	return org.ValidID(m.OrganizationID) && m.Principal.Validate() == nil && m.Principal.Kind == authorization.User && (m.Role == authorization.RoleOwner || m.Role == authorization.RoleViewer)
}
func (s *Postgres) CreateMembership(ctx context.Context, m authorization.Membership, created time.Time) error {
	if !validMember(m) || !org.ValidTime(created) {
		return authorization.ErrInvalidMembership
	}
	return s.exec(ctx, `INSERT INTO organization_members (organization_id,user_id,role,active,created_at) VALUES ($1,$2,$3,$4,$5)`, m.OrganizationID, m.Principal.ID, string(m.Role), m.Active, created.UTC())
}
func (s *Postgres) UpdateMembership(ctx context.Context, m authorization.Membership) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if !validMember(m) {
		return authorization.ErrInvalidMembership
	}
	result, err := s.db.ExecContext(ctx, `UPDATE organization_members SET role=$3,active=$4 WHERE organization_id=$1 AND user_id=$2`, m.OrganizationID, m.Principal.ID, string(m.Role), m.Active)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return authorization.ErrMembershipNotFound
	}
	return nil
}
func (s *Postgres) GetMembership(ctx context.Context, organizationID string, p authorization.Principal) (authorization.Membership, error) {
	if err := s.check(ctx); err != nil {
		return authorization.Membership{}, err
	}
	if !org.ValidID(organizationID) || p.Validate() != nil {
		return authorization.Membership{}, authorization.ErrInvalidMembership
	}
	// User IDs cannot collide with an unsupported service-account principal.
	if p.Kind != authorization.User {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	m := authorization.Membership{Principal: authorization.Principal{Kind: authorization.User}}
	err := s.db.QueryRowContext(ctx, `SELECT m.organization_id,m.user_id,m.role,(m.active AND u.active AND o.state='ACTIVE')
FROM organization_members m JOIN users u ON u.user_id=m.user_id JOIN organizations o ON o.organization_id=m.organization_id
WHERE m.organization_id=$1 AND m.user_id=$2`, organizationID, p.ID).Scan(&m.OrganizationID, &m.Principal.ID, &m.Role, &m.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return authorization.Membership{}, authorization.ErrMembershipNotFound
	}
	if err != nil {
		return authorization.Membership{}, err
	}
	if !validMember(m) || m.OrganizationID != organizationID || m.Principal != p {
		return authorization.Membership{}, authorization.ErrInvalidMembership
	}
	return m, nil
}

// Resolve implements application.TenantResolver for repository and organization
// scopes. It reloads active membership and active project/repository ownership.
// Project management uses GetProject plus organization-level authorization.
func (s *Postgres) Resolve(ctx context.Context, p authorization.Principal, target tenant.Scope) (tenant.Scope, error) {
	if target.Validate() != nil {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	m, err := s.GetMembership(ctx, target.OrganizationID, p)
	if err != nil {
		return tenant.Scope{}, err
	}
	if !m.Active {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	if target.ResourceID == target.OrganizationID {
		return target, nil
	}
	var id string
	err = s.db.QueryRowContext(ctx, `SELECT r.repository_id FROM repositories r
JOIN projects p ON p.organization_id=r.organization_id AND p.project_id=r.project_id
WHERE r.organization_id=$1 AND r.repository_id=$2 AND r.active AND p.active`, target.OrganizationID, target.ResourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	if err != nil {
		return tenant.Scope{}, err
	}
	if id != target.ResourceID {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return target, nil
}

type OrganizationPage struct {
	Organizations []org.Organization
	NextAfterID   string
}

func (s *Postgres) ListOrganizations(ctx context.Context, p authorization.Principal, after string, limit int) (OrganizationPage, error) {
	failure := OrganizationPage{Organizations: []org.Organization{}}
	if err := s.check(ctx); err != nil {
		return failure, err
	}
	if p.Validate() != nil || p.Kind != authorization.User || (after != "" && !org.ValidID(after)) || limit < 1 || limit > org.MaxPageSize {
		return failure, org.ErrInvalidEntity
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.organization_id,o.name,o.state,o.created_at FROM organizations o
JOIN organization_members m ON m.organization_id=o.organization_id JOIN users u ON u.user_id=m.user_id
WHERE m.user_id=$1 AND m.active AND u.active AND o.state='ACTIVE' AND o.organization_id COLLATE "C" > $2 COLLATE "C"
ORDER BY o.organization_id COLLATE "C" LIMIT $3`, p.ID, after, limit+1)
	if err != nil {
		return failure, err
	}
	defer rows.Close()
	page := OrganizationPage{Organizations: []org.Organization{}}
	previous := after
	for rows.Next() {
		var o org.Organization
		if err := rows.Scan(&o.OrganizationID, &o.Name, &o.State, &o.CreatedAt); err != nil {
			return failure, err
		}
		if o.Validate() != nil || o.State != org.Active || o.OrganizationID <= previous || len(page.Organizations) > limit {
			return failure, org.ErrData
		}
		previous = o.OrganizationID
		page.Organizations = append(page.Organizations, o)
	}
	if err := rows.Err(); err != nil {
		return failure, err
	}
	if len(page.Organizations) > limit {
		page.Organizations = page.Organizations[:limit]
		page.NextAfterID = page.Organizations[limit-1].OrganizationID
	}
	return page, nil
}
func (s *Postgres) CreateSubscription(ctx context.Context, organizationID string, v org.Subscription) error {
	if v.Validate() != nil || v.OrganizationID != organizationID || v.Revision != 1 {
		return org.ErrInvalidEntity
	}
	return s.exec(ctx, `INSERT INTO subscriptions (organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, organizationID, v.SubscriptionID, v.EditionVersionID, v.Provider, v.ExternalReference, string(v.State), v.Revision, v.CreatedAt.UTC(), v.UpdatedAt.UTC())
}
func (s *Postgres) GetSubscription(ctx context.Context, organizationID, id string) (org.Subscription, error) {
	if err := s.check(ctx); err != nil {
		return org.Subscription{}, err
	}
	if !scoped(organizationID, id) {
		return org.Subscription{}, org.ErrInvalidEntity
	}
	var v org.Subscription
	err := s.db.QueryRowContext(ctx, `SELECT organization_id,subscription_id,edition_version_id,provider,external_reference,state,revision,created_at,updated_at FROM subscriptions WHERE organization_id=$1 AND subscription_id=$2`, organizationID, id).Scan(&v.OrganizationID, &v.SubscriptionID, &v.EditionVersionID, &v.Provider, &v.ExternalReference, &v.State, &v.Revision, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return org.Subscription{}, storageError(err)
	}
	if v.Validate() != nil || v.OrganizationID != organizationID || v.SubscriptionID != id {
		return org.Subscription{}, org.ErrData
	}
	return v, nil
}

// CAS updates cannot move a subscription or replace its provider identity. A
// bundle change does not rewrite entitlements, roles, repositories or usage.
func (s *Postgres) UpdateSubscription(ctx context.Context, organizationID string, v org.Subscription, expected int64) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	if v.Validate() != nil || v.OrganizationID != organizationID || expected < 1 || expected == math.MaxInt64 || v.Revision != expected+1 {
		return org.ErrInvalidEntity
	}
	result, err := s.db.ExecContext(ctx, `UPDATE subscriptions SET edition_version_id=$3,state=$4,revision=$5,updated_at=$6
WHERE organization_id=$1 AND subscription_id=$2 AND revision=$7 AND provider=$8 AND external_reference=$9 AND created_at=$10 AND updated_at<=$6`, organizationID, v.SubscriptionID, v.EditionVersionID, string(v.State), v.Revision, v.UpdatedAt.UTC(), expected, v.Provider, v.ExternalReference, v.CreatedAt.UTC())
	if err != nil {
		return storageError(err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return org.ErrConflict
	}
	return nil
}
