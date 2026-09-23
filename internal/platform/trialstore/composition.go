package trialstore

import (
	"database/sql"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/trial"
	"safepatch/internal/platform/organizationstore"
	"time"
)

// NewManagement wires current organization membership and billing.manage checks.
// It does no provisioning or network I/O. Pass principals from authentication.
func NewManagement(db *sql.DB, policy *trial.Policy, features *feature.FeatureCatalog, now func() time.Time) (*application.TrialService, error) {
	store, err := NewPostgres(db, policy, features, now)
	if err != nil {
		return nil, err
	}
	organizations, err := organizationstore.NewPostgres(db, features)
	if err != nil {
		return nil, err
	}
	permissions, err := authorization.NewPermissionService(organizations)
	if err != nil {
		return nil, err
	}
	return application.NewTrialService(store, organizations, permissions)
}
