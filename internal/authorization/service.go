package authorization

import (
	"context"
	"errors"
	"fmt"

	"safepatch/internal/tenant"
)

type ActorKind string

const (
	User           ActorKind = "user"
	ServiceAccount ActorKind = "service_account"
)

// Principal must come from trusted authentication, not request role claims.
type Principal struct {
	ID   string
	Kind ActorKind
}

func (p Principal) Validate() error {
	if !tenant.ValidID(p.ID) || (p.Kind != User && p.Kind != ServiceAccount) {
		return ErrInvalidPrincipal
	}
	return nil
}

type Membership struct {
	OrganizationID string
	Principal      Principal
	Role           Role
	Active         bool
}

var (
	ErrInvalidPrincipal   = errors.New("invalid authenticated principal")
	ErrInvalidMembership  = errors.New("invalid organization membership")
	ErrMembershipNotFound = errors.New("organization membership not found")
	ErrInvalidService     = errors.New("permission service is not configured")
	ErrInvalidRequest     = errors.New("invalid permission request")
)

// MembershipRepository reads current authoritative membership by organization,
// actor kind, AND actor ID. Filter in storage; never return another tenant's row.
// Missing memberships return ErrMembershipNotFound. Deleted/disabled members
// must not retain roles through a stale cache. Implementations are concurrency
// safe, honor contexts, and return storage failures as errors, not empty grants.
// Membership changes require a separately authorized writer; no write API here.
type MembershipRepository interface {
	GetMembership(context.Context, string, Principal) (Membership, error)
}

type PermissionChecker interface {
	Allowed(context.Context, Principal, string, Key) (bool, error)
}

// PermissionService checks organization-wide actor permissions only. It has no
// edition, subscription, billing, or entitlement input and retains no role cache.
// Resource ownership is established by tenant resolution before this check.
type PermissionService struct{ memberships MembershipRepository }

var _ PermissionChecker = (*PermissionService)(nil)

func NewPermissionService(memberships MembershipRepository) (*PermissionService, error) {
	if memberships == nil {
		return nil, ErrInvalidService
	}
	return &PermissionService{memberships: memberships}, nil
}

func (s *PermissionService) Allowed(ctx context.Context, actor Principal, organizationID string, key Key) (bool, error) {
	if s == nil || s.memberships == nil {
		return false, ErrInvalidService
	}
	if ctx == nil || !tenant.ValidID(organizationID) {
		return false, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := actor.Validate(); err != nil {
		return false, err
	}
	if err := ValidateKey(key); err != nil {
		return false, err
	}
	membership, err := s.memberships.GetMembership(ctx, organizationID, actor)
	if cancelled := ctx.Err(); cancelled != nil {
		return false, cancelled
	}
	if errors.Is(err, ErrMembershipNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read organization membership: %w", err)
	}
	if membership.OrganizationID != organizationID || membership.Principal != actor || (membership.Role != RoleOwner && membership.Role != RoleViewer) {
		return false, ErrInvalidMembership
	}
	if !membership.Active {
		return false, nil
	}
	return roleAllows(membership.Role, key), nil
}
