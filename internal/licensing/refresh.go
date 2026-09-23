package licensing

import (
	"context"
	"errors"
	"slices"
	"time"
)

var (
	ErrRefreshUnavailable  = errors.New("license refresh temporarily unavailable")
	ErrRefreshRejected     = errors.New("license refresh rejected")
	ErrLicenseConflict     = errors.New("license installation changed")
	ErrLicenseNotInstalled = errors.New("license not installed")
)

// LicenseStateStore is trusted operator-owned state. CompareAndSwapLicense must
// persist atomically and reject a changed revision, including concurrent install
// or revocation. Expected zero inserts only when no installation exists.
type LicenseStateStore interface {
	LicenseSource
	CompareAndSwapLicense(context.Context, string, uint64, LicenseState) error
}
type RefreshResult struct {
	Status   string // active, revoked or denied; unknown is never active
	Envelope []byte // required for active; still independently signature-verified
}

// LicenseRefreshClient authenticates status from the configured issuer. Only
// ErrRefreshUnavailable classifies retryable transport/availability failures.
// Authentication failures and invalid responses must return ErrRefreshRejected.
type LicenseRefreshClient interface {
	RefreshLicense(context.Context, string, string) (RefreshResult, error)
}
type RefreshConfig struct {
	Provider      ProviderConfig
	Store         LicenseStateStore
	Client        LicenseRefreshClient
	RetryInterval time.Duration
	Timeout       time.Duration
	// Observe receives sanitized refresh outcomes, including outages during grace.
	Observe func(error)
}
type LicenseRefresher struct {
	verifier       *EnterpriseLicenseEntitlementProvider
	store          LicenseStateStore
	client         LicenseRefreshClient
	retry, timeout time.Duration
	observe        func(error)
}

func NewLicenseRefresher(c RefreshConfig) (*LicenseRefresher, error) {
	// Air-gapped nodes cannot construct a refresher, even with an injected client.
	if c.Provider.Deployment.Mode != Connected || c.Store == nil || c.Client == nil || c.RetryInterval < time.Second || c.RetryInterval > c.Provider.Deployment.RefreshInterval || c.Timeout < time.Millisecond || c.Timeout > 30*time.Second {
		return nil, ErrInvalidLicense
	}
	c.Provider.Source = c.Store
	p, err := NewEnterpriseLicenseEntitlementProvider(c.Provider)
	if err != nil {
		return nil, err
	}
	return &LicenseRefresher{p, c.Store, c.Client, c.RetryInterval, c.Timeout, c.Observe}, nil
}

// RefreshOnce is deliberately separate from Has/Limit: remote latency never sits
// on the verification admission path. It can renew an expired signed license.
func (r *LicenseRefresher) RefreshOnce(ctx context.Context) error {
	if r == nil || r.verifier == nil || ctx == nil {
		return ErrInvalidLicense
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p := r.verifier
	state, err := r.store.LoadLicense(ctx, p.org)
	if err != nil {
		return ErrLicenseUnavailable
	}
	if state.Revision == 0 || state.Revision >= 1<<63-1 {
		return ErrInvalidLicense
	}
	// Revocation is sticky until a trusted operator installs a newer state.
	// An online active response must not undo a local operator revocation.
	if state.Revoked {
		return ErrRefreshRejected
	}
	now := p.now().UTC().Truncate(time.Microsecond)
	if now.IsZero() || now.Year() < 1 || now.Year() > 9999 || state.ConfirmedAt.After(now) {
		return ErrInvalidLicense
	}
	// Read identity only from a signed document. Historical signature validation
	// here permits renewal; it is never returned as a runtime entitlement.
	var envelope SignedLicense
	var doc LicenseDocument
	if strict(state.Envelope, &envelope) != nil || strict(envelope.Document, &doc) != nil || now.Before(doc.IssuedAt) {
		return ErrInvalidLicense
	}
	if _, err = verify(state.Envelope, p.keys, p.features, p.editions, p.org, doc.IssuedAt); err != nil {
		return err
	}
	if !state.RefreshDenied && !state.ConfirmedAt.IsZero() && now.Before(doc.ExpiresAt) && now.Before(state.ConfirmedAt.Add(p.deployment.RefreshInterval)) {
		return nil
	}
	request, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	result, refreshErr := r.client.RefreshLicense(request, p.org, doc.LicenseID)
	if err := ctx.Err(); err != nil {
		return err
	}
	denied := errors.Is(refreshErr, ErrRefreshRejected) || refreshErr == nil && (result.Status == "revoked" || result.Status == "denied")
	if !denied && (errors.Is(refreshErr, ErrRefreshUnavailable) || errors.Is(request.Err(), context.DeadlineExceeded)) {
		// No write: outages cannot reset the persisted grace anchor or a denial.
		return ErrRefreshUnavailable
	}
	next := state
	next.Revision++
	next.Envelope = slices.Clone(state.Envelope)
	next.RefreshDenied = false
	completed := p.now().UTC().Truncate(time.Microsecond)
	if completed.Before(now) || completed.Year() > 9999 {
		return ErrInvalidLicense
	}
	if refreshErr != nil {
		result.Status = "denied"
	}
	switch result.Status {
	case "active":
		if _, err = verify(result.Envelope, p.keys, p.features, p.editions, p.org, completed); err != nil {
			next.RefreshDenied = true
			refreshErr = ErrRefreshRejected
		} else {
			next.Envelope = slices.Clone(result.Envelope)
			next.Revoked = false
			next.ConfirmedAt = completed
		}
	case "revoked":
		next.Revoked = true
		refreshErr = ErrRefreshRejected
	default:
		next.RefreshDenied = true
		refreshErr = ErrRefreshRejected
	}
	if err = r.store.CompareAndSwapLicense(ctx, p.org, state.Revision, next); err != nil {
		if errors.Is(err, ErrLicenseConflict) {
			return ErrLicenseConflict
		}
		return ErrLicenseUnavailable
	}
	return refreshErr
}

// Run is an optional, context-owned background loop. Composition launches it
// only for connected deployments. Offline providers do not need this worker.
func (r *LicenseRefresher) Run(ctx context.Context) error {
	if r == nil || r.verifier == nil || ctx == nil {
		return ErrInvalidLicense
	}
	ticker := time.NewTicker(r.retry)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := r.RefreshOnce(ctx)
		if r.observe != nil {
			r.observe(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
