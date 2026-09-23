package billingstore

import (
	"database/sql"
	"net/http"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/billing/stripe"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/feature"
	"safepatch/internal/platform/httpapi"
	"safepatch/internal/platform/organizationstore"
	"time"
)

// StripeSubscriptions is shared composition, not an edition-specific server.
// Callers mount Webhook and inject Entitlements into existing operation guards.
// Construction performs no network request, provisioning write or migration.
type StripeSubscriptions struct {
	Store           *Postgres
	Adapter         *stripe.StripeBillingAdapter
	Synchronization *stripe.SubscriptionIntegration
	Entitlements    *RuntimeEntitlements
	Management      *application.BillingService
	Webhook         http.Handler
}

func NewStripeSubscriptions(db *sql.DB, c Connection, cfg stripe.Config, features *feature.FeatureCatalog, fallbackVersion string, now func() time.Time) (*StripeSubscriptions, error) {
	if c.Provider != stripe.Provider || c.LiveMode != cfg.LiveMode {
		return nil, billing.ErrInvalidConfiguration
	}
	store, err := NewPostgres(db, c, features, now)
	if err != nil {
		return nil, err
	}
	// Wire the same account/environment-scoped directory and clock everywhere.
	cfg.Customers = store
	cfg.Now = now
	adapter, err := stripe.NewStripeBillingAdapter(cfg)
	if err != nil {
		return nil, err
	}
	sync, err := stripe.NewSubscriptionIntegration(adapter, store)
	if err != nil {
		return nil, err
	}
	runtime, err := NewRuntimeEntitlements(store, fallbackVersion)
	if err != nil {
		return nil, err
	}
	organizationStore, err := organizationstore.NewPostgres(db, features)
	if err != nil {
		return nil, err
	}
	permissions, err := authorization.NewPermissionService(organizationStore)
	if err != nil {
		return nil, err
	}
	management, err := application.NewBillingService(adapter, organizationStore, permissions)
	if err != nil {
		return nil, err
	}
	webhook, err := httpapi.NewBillingWebhookHandler(sync)
	if err != nil {
		return nil, err
	}
	return &StripeSubscriptions{store, adapter, sync, runtime, management, webhook}, nil
}
