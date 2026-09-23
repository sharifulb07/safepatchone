package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"safepatch/internal/audit"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	distribution "safepatch/internal/domain/release"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
)

// PlatformAccess uses the same dependencies as ordinary execution. Callers must
// supply authenticated principals; no role/edition claims are accepted here.
type PlatformAccess struct {
	Tenants      TenantResolver
	Entitlements entitlement.EntitlementService
	Permissions  authorization.PermissionChecker
	Quotas       QuotaChecker
}

func (a PlatformAccess) guard(key feature.Key, permission authorization.Key) (*ExecutionGuard, error) {
	return NewExecutionGuard(a.Tenants, a.Entitlements, a.Permissions, a.Quotas, Requirement{Feature: key, Permission: permission})
}

// SSOService decorates an existing trusted identity adapter. That adapter
// must verify protocol signatures, issuer/audience, replay/expiry and explicitly
// linked identities. It must never turn an email or request user ID into identity.
// This service is bound to one organization and rechecks membership and
// ownership on every call. It neither creates users nor assigns roles/sessions.
type SSOService struct {
	org       string
	operation *GuardedOperation
}

type SSOIdentity struct {
	OrganizationID string                  `json:"organization_id"`
	Actor          authorization.Principal `json:"actor"`
}

func NewSSOService(org string, identity Authenticator, access PlatformAccess) (*SSOService, error) {
	if !tenant.ValidID(org) {
		return nil, ErrInvalidGuard
	}
	g, err := NewGuardedOperation(identity, access.Tenants, access.Entitlements, access.Permissions, access.Quotas, Requirement{Feature: feature.KeyEnterpriseSSO, Permission: authorization.KeyProofView}, func(context.Context, Execution) error { return nil })
	if err != nil {
		return nil, err
	}
	return &SSOService{org, g}, nil
}

// The organization binding must be retained by the shared session/request
// adapter. Never promote this result into an unrestricted global login session.
func (s *SSOService) Authenticate(ctx context.Context, credential string) (SSOIdentity, error) {
	if s == nil || s.operation == nil {
		return SSOIdentity{}, ErrInvalidGuard
	}
	x, err := s.operation.Authorize(ctx, Request{Credential: credential, Target: tenant.Scope{OrganizationID: s.org, ResourceID: s.org}})
	if err != nil {
		return SSOIdentity{}, err
	}
	if x.Actor.Kind != authorization.User {
		return SSOIdentity{}, authorization.ErrInvalidPrincipal
	}
	return SSOIdentity{OrganizationID: s.org, Actor: x.Actor}, nil
}

type AuditService struct {
	read, export *ExecutionGuard
	store        audit.Store
}
type AuditExport struct {
	Schema         string     `json:"schema"`
	OrganizationID string     `json:"organization_id"`
	Page           audit.Page `json:"page"`
	Digest         string     `json:"digest"`
}

func NewAuditService(access PlatformAccess, store audit.Store) (*AuditService, error) {
	if store == nil {
		return nil, ErrInvalidGuard
	}
	r, err := access.guard(feature.KeyAuditOrganization, authorization.KeyAuditView)
	if err != nil {
		return nil, err
	}
	e, err := access.guard(feature.KeyEnterpriseAudit, authorization.KeyOrganizationManage)
	if err != nil {
		return nil, err
	}
	return &AuditService{r, e, store}, nil
}
func (s *AuditService) page(ctx context.Context, actor authorization.Principal, org string, q audit.Query, export bool) (audit.Page, error) {
	if s == nil || s.store == nil || q.Validate() != nil {
		return audit.Page{}, audit.ErrInvalid
	}
	g := s.read
	if export {
		g = s.export
	}
	if _, err := g.Authorize(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: org}); err != nil {
		return audit.Page{}, err
	}
	p, err := s.store.List(ctx, org, q)
	if err != nil {
		return audit.Page{}, audit.ErrUnavailable
	}
	if err = ctx.Err(); err != nil {
		return audit.Page{}, err
	}
	if p.Events == nil || len(p.Events) > q.Limit {
		return audit.Page{}, audit.ErrInvalid
	}
	previous := q.AfterID
	for _, event := range p.Events {
		if event.Validate() != nil || event.OrganizationID != org || event.ID <= previous {
			return audit.Page{}, audit.ErrInvalid
		}
		previous = event.ID
	}
	if p.NextAfterID != "" && (len(p.Events) != q.Limit || p.NextAfterID != previous) {
		return audit.Page{}, audit.ErrInvalid
	}
	return p, nil
}
func (s *AuditService) Read(ctx context.Context, actor authorization.Principal, org string, q audit.Query) (audit.Page, error) {
	return s.page(ctx, actor, org, q, false)
}
func (s *AuditService) Export(ctx context.Context, actor authorization.Principal, org string, q audit.Query) (AuditExport, error) {
	p, err := s.page(ctx, actor, org, q, true)
	if err != nil {
		return AuditExport{}, err
	}
	// Bind tenant and schema as well as page bytes; this digest is integrity
	// metadata, not a digital signature or proof of completeness.
	raw, err := json.Marshal(struct {
		Schema         string     `json:"schema"`
		OrganizationID string     `json:"organization_id"`
		Page           audit.Page `json:"page"`
	}{"safepatch.audit-export.v1", org, p})
	if err != nil {
		return AuditExport{}, audit.ErrInvalid
	}
	return AuditExport{"safepatch.audit-export.v1", org, p, evidence.Digest(raw)}, nil
}

// Release is catalog metadata independent of edition and edition version.
// One release catalog can associate the same product version with any channel.
type Release struct {
	Version        string    `json:"version"`
	Channel        string    `json:"channel"`
	SupportedUntil time.Time `json:"supported_until"`
}

var ErrReleaseUnavailable = errors.New("release support unavailable")

type ReleaseService struct {
	stable, lts *ExecutionGuard
	catalog     map[string]Release
	now         func() time.Time
}

func NewReleaseService(access PlatformAccess, releases []Release, now func() time.Time) (*ReleaseService, error) {
	if now == nil || len(releases) == 0 || len(releases) > 4096 {
		return nil, ErrInvalidGuard
	}
	stable, err := access.guard(feature.KeyVerifyBasic, authorization.KeyOrganizationManage)
	if err != nil {
		return nil, err
	}
	lts, err := access.guard(feature.KeyEnterpriseLTS, authorization.KeyOrganizationManage)
	if err != nil {
		return nil, err
	}
	s := &ReleaseService{stable, lts, map[string]Release{}, now}
	for _, r := range releases {
		channel, err := distribution.ParseChannel(r.Channel)
		if err != nil {
			return nil, ErrReleaseUnavailable
		}
		r.Channel = string(channel)
		key := r.Version + "/" + r.Channel
		if !distribution.ValidVersion(r.Version) || r.SupportedUntil.IsZero() || r.SupportedUntil.Year() < 1 || r.SupportedUntil.Year() > 9999 || s.catalog[key].Version != "" {
			return nil, ErrReleaseUnavailable
		}
		s.catalog[key] = r
	}
	return s, nil
}
func (s *ReleaseService) Eligible(ctx context.Context, actor authorization.Principal, org, version, channel string) (Release, error) {
	if s == nil || s.now == nil {
		return Release{}, ErrInvalidGuard
	}
	canonical, err := distribution.ParseChannel(channel)
	if !distribution.ValidVersion(version) || err != nil {
		return Release{}, ErrReleaseUnavailable
	}
	channel = string(canonical)
	r, ok := s.catalog[version+"/"+channel]
	if !ok {
		return Release{}, ErrReleaseUnavailable
	}
	g := s.stable
	if canonical == distribution.LTS {
		g = s.lts
	}
	if _, err := g.Authorize(ctx, actor, tenant.Scope{OrganizationID: org, ResourceID: org}); err != nil {
		return Release{}, err
	}
	now := s.now()
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 || !now.Before(r.SupportedUntil) {
		return Release{}, ErrReleaseUnavailable
	}
	return r, nil
}

// DeploymentAccess is additional admission to the existing ChangeWorkflow. It
// never changes its verifier, policy, evidence or UNKNOWN behavior. Air gap is a
// trusted topology choice; network isolation remains the deployment's duty.
type DeploymentAccess struct{ guards []*ExecutionGuard }

func NewDeploymentAccess(access PlatformAccess, airGapped bool) (*DeploymentAccess, error) {
	keys := []feature.Key{feature.KeyEnterpriseSelfHosted}
	if airGapped {
		keys = append(keys, feature.KeyEnterpriseAirgap)
	}
	d := &DeploymentAccess{}
	for _, key := range keys {
		g, err := access.guard(key, authorization.KeyProofExecute)
		if err != nil {
			return nil, err
		}
		d.guards = append(d.guards, g)
	}
	return d, nil
}
func (d *DeploymentAccess) Authorize(ctx context.Context, actor authorization.Principal, scope tenant.Scope) error {
	if d == nil || len(d.guards) == 0 {
		return ErrInvalidGuard
	}
	for _, g := range d.guards {
		if _, err := g.authorizeAccess(ctx, actor, scope); err != nil {
			return err
		}
	}
	return nil // the shared workflow reserves verification quota once
}

type deploymentRunner struct {
	runner verification.Runner
	access *DeploymentAccess
	actor  authorization.Principal
	scope  tenant.Scope
}

func (r deploymentRunner) Run(ctx context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	if err := r.access.Authorize(ctx, r.actor, r.scope); err != nil {
		return verification.ExecutionResult{Status: verification.Unknown, ExitCode: -1}, err
	}
	return r.runner.Run(ctx, request)
}
