package entitlement

import (
	"context"
	"errors"

	domain "safepatch/internal/domain/entitlement"
)

var (
	ErrNotFound             = errors.New("entitlement not found")
	ErrAlreadyExists        = errors.New("entitlement already exists")
	ErrInvalidQuery         = errors.New("invalid entitlement repository query")
	ErrOrganizationMismatch = errors.New("entitlement organization does not match scope")
)

const MaxPageSize = 200

// EntitlementRepository persists canonical records without provider objects.
//
// Every operation requires validated organization scope. Adapters must enforce
// that scope in storage queries, not filter unscoped results after retrieval.
// Get must call ValidateGet and return ErrNotFound for both missing and other-
// organization records. Create must call ValidateCreate, validate organization/
// feature references, and atomically reject an existing (organization_id,
// entitlement_id) without upserting. Multiple records/sources for one feature
// are allowed. List must call ValidateList and include disabled, future, and
// expired records; it must not perform precedence or effective-grant merging.
//
// Implementations must honor cancellation/deadlines, return storage failures
// as errors rather than successful empty results, and copy mutable values.
// Organization membership and actor authorization require separate services.
// No unscoped Get, organization enumeration, Update, or Delete is exposed.
type EntitlementRepository interface {
	Create(ctx context.Context, organizationID string, record domain.Entitlement) error
	Get(ctx context.Context, organizationID, entitlementID string) (domain.Entitlement, error)
	List(ctx context.Context, organizationID string, query ListQuery) (Page, error)
}

// ListQuery uses bounded keyset pagination within one organization. FeatureID
// is an optional exact filter. AfterID is the last returned entitlement ID.
// Adapters must use deterministic binary ID ordering (e.g. PostgreSQL COLLATE C).
// Limit must be explicit, 1..MaxPageSize. Pagination is not a transaction snapshot.
type ListQuery struct {
	FeatureID string
	AfterID   string
	Limit     int
}

type Page struct {
	Entitlements []domain.Entitlement
	// Empty means exhausted. Otherwise the last returned ID; callers must keep
	// the same organization and feature filter when requesting the next page.
	NextAfterID string
}

func ValidateCreate(organizationID string, record domain.Entitlement) error {
	if !domain.ValidID(organizationID) {
		return ErrInvalidQuery
	}
	if organizationID != record.OrganizationID {
		return ErrOrganizationMismatch
	}
	return record.Validate()
}

func ValidateGet(organizationID, entitlementID string) error {
	if !domain.ValidID(organizationID) || !domain.ValidID(entitlementID) {
		return ErrInvalidQuery
	}
	return nil
}

func ValidateList(organizationID string, query ListQuery) error {
	if !domain.ValidID(organizationID) || query.Limit < 1 || query.Limit > MaxPageSize {
		return ErrInvalidQuery
	}
	if query.FeatureID != "" && !domain.ValidID(query.FeatureID) {
		return ErrInvalidQuery
	}
	if query.AfterID != "" && !domain.ValidID(query.AfterID) {
		return ErrInvalidQuery
	}
	return nil
}
