package application

import (
	"context"
	"fmt"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/organization"
	"safepatch/internal/domain/trial"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"slices"
)

type OnboardingGitHub interface {
	Repositories(context.Context, authorization.Principal, int64) ([]flow.Repository, error)
}
type OnboardingBilling interface {
	Checkout(context.Context, authorization.Principal, billing.CheckoutRequest) (billing.Session, error)
}
type OnboardingTrials interface {
	Start(context.Context, authorization.Principal, string, edition.Key) (trial.Trial, error)
}
type FirstProof struct {
	ID     string `json:"id"`
	Status string `json:"status"` // PENDING, QUEUED, RUNNING, PASS, FAIL, UNKNOWN, ERROR
}
type OnboardingProofs interface {
	Submit(context.Context, authorization.Principal, flow.State, string) (FirstProof, error)
	Read(context.Context, string, string) (FirstProof, error)
}
type OnboardingOffer struct {
	Edition                    edition.Key
	VersionID, CheckoutOfferID string
}
type OnboardingPolicy struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Feature     feature.Key `json:"-"`
}
type OnboardingView struct {
	State    flow.State         `json:"state"`
	Access   flow.Access        `json:"access"`
	Policies []OnboardingPolicy `json:"policies"`
	Proof    *FirstProof        `json:"proof,omitempty"`
}
type OnboardingService struct {
	store        flow.Store
	tenants      TenantResolver
	permissions  authorization.PermissionChecker
	entitlements entitlement.EntitlementService
	github       OnboardingGitHub
	billing      OnboardingBilling
	trials       OnboardingTrials
	proofs       OnboardingProofs
	offers       map[edition.Key]OnboardingOffer
	policies     map[string]OnboardingPolicy
}

func NewOnboardingService(store flow.Store, tenants TenantResolver, permissions authorization.PermissionChecker, entitlements entitlement.EntitlementService, github OnboardingGitHub, billing OnboardingBilling, trials OnboardingTrials, proofs OnboardingProofs, offers []OnboardingOffer, policies []OnboardingPolicy) (*OnboardingService, error) {
	if store == nil || tenants == nil || permissions == nil || entitlements == nil || github == nil || trials == nil || proofs == nil || len(offers) != 5 || len(policies) == 0 || len(policies) > 32 {
		return nil, flow.ErrInvalid
	}
	s := &OnboardingService{store, tenants, permissions, entitlements, github, billing, trials, proofs, map[edition.Key]OnboardingOffer{}, map[string]OnboardingPolicy{}}
	for _, o := range offers {
		if _, err := edition.Lookup(o.Edition); err != nil || !organization.ValidID(o.VersionID) || s.offers[o.Edition].VersionID != "" {
			return nil, flow.ErrInvalid
		}
		s.offers[o.Edition] = o
	}
	for _, p := range policies {
		if !organization.ValidID(p.ID) || p.Name == "" || feature.ValidateKey(p.Feature) != nil || s.policies[p.ID].ID != "" {
			return nil, flow.ErrInvalid
		}
		s.policies[p.ID] = p
	}
	return s, nil
}
func (s *OnboardingService) authorize(ctx context.Context, actor authorization.Principal, org string, key authorization.Key) error {
	if s == nil || ctx == nil || actor.Validate() != nil || actor.Kind != authorization.User || !organization.ValidID(org) {
		return flow.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	want := tenant.Scope{OrganizationID: org, ResourceID: org}
	got, err := s.tenants.Resolve(ctx, actor, want)
	if err == nil && got != want {
		err = tenant.ErrInvalidScope
	}
	if err = stageResult(ctx, "tenant", err); err != nil {
		return err
	}
	ok, err := s.permissions.Allowed(ctx, actor, org, key)
	if err == nil && !ok {
		err = ErrPermissionDenied
	}
	return stageResult(ctx, "permission", err)
}
func (s *OnboardingService) Create(ctx context.Context, actor authorization.Principal, attempt, name string) (flow.State, error) {
	if s == nil || ctx == nil || actor.Validate() != nil || actor.Kind != authorization.User || !organization.ValidID(attempt) {
		return flow.State{}, flow.ErrInvalid
	}
	v, err := s.store.Create(ctx, actor.ID, attempt, name)
	if err == nil && v.Validate() != nil {
		err = flow.ErrInvalid
	}
	if err = stageResult(ctx, "organization", err); err != nil {
		return flow.State{}, err
	}
	return v, nil
}
func (s *OnboardingService) load(ctx context.Context, org string) (flow.State, error) {
	v, err := s.store.Load(ctx, org)
	if err == nil && (v.Validate() != nil || v.OrganizationID != org) {
		err = flow.ErrInvalid
	}
	return v, err
}
func (s *OnboardingService) ready(ctx context.Context, v flow.State) error {
	if v.EditionVersionID == "" {
		return flow.ErrNotReady
	}
	a, err := s.store.Access(ctx, v.OrganizationID, v.EditionVersionID)
	if err != nil {
		return err
	}
	if !a.Ready {
		return flow.ErrNotReady
	}
	return nil
}
func (s *OnboardingService) View(ctx context.Context, actor authorization.Principal, org string) (OnboardingView, error) {
	if err := s.authorize(ctx, actor, org, authorization.KeyProofView); err != nil {
		return OnboardingView{}, err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return OnboardingView{}, err
	}
	result := OnboardingView{State: v, Policies: []OnboardingPolicy{}}
	if v.EditionVersionID != "" {
		result.Access, err = s.store.Access(ctx, org, v.EditionVersionID)
		if err != nil {
			return OnboardingView{}, err
		}
	}
	for _, p := range s.policies {
		ok, err := s.entitlements.Has(ctx, org, p.Feature)
		if err != nil {
			return OnboardingView{}, err
		}
		if ok {
			result.Policies = append(result.Policies, p)
		}
	}
	slices.SortFunc(result.Policies, func(a, b OnboardingPolicy) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if v.ProofID != "" {
		p, err := s.proofs.Read(ctx, org, v.ProofID)
		if err != nil || p.ID != v.ProofID {
			return OnboardingView{}, flow.ErrUnavailable
		}
		result.Proof = &p
	}
	return result, nil
}
func (s *OnboardingService) SelectEdition(ctx context.Context, actor authorization.Principal, org string, revision int64, key edition.Key) (flow.State, error) {
	if err := s.authorize(ctx, actor, org, authorization.KeyBillingManage); err != nil {
		return flow.State{}, err
	}
	o, ok := s.offers[key]
	if !ok {
		return flow.State{}, flow.ErrInvalid
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return flow.State{}, err
	}
	if v.Revision != revision {
		return flow.State{}, flow.ErrConflict
	}
	if v.Edition == key {
		return v, nil
	}
	if v.ProofID != "" {
		return flow.State{}, flow.ErrConflict
	}
	v.Edition = key
	v.EditionVersionID = o.VersionID
	// Selection changes intent only. It never writes subscriptions or grants.
	return s.store.Save(ctx, v, revision)
}
func (s *OnboardingService) StartBilling(ctx context.Context, actor authorization.Principal, org string, revision int64, mode string) (string, error) {
	if err := s.authorize(ctx, actor, org, authorization.KeyBillingManage); err != nil {
		return "", err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return "", err
	}
	if v.Revision != revision {
		return "", flow.ErrConflict
	}
	o, ok := s.offers[v.Edition]
	if !ok {
		return "", flow.ErrNotReady
	}
	switch mode {
	case "portal":
		portal, ok := s.billing.(interface {
			Portal(context.Context, authorization.Principal, billing.PortalRequest) (billing.Session, error)
		})
		if !ok {
			return "", flow.ErrUnavailable
		}
		session, err := portal.Portal(ctx, actor, billing.PortalRequest{OrganizationID: org, IdempotencyKey: fmt.Sprintf("portal_%s_%d", org, revision)})
		if err != nil {
			return "", err
		}
		return session.URL, nil
	case "trial":
		_, err = s.trials.Start(ctx, actor, org, v.Edition)
		return "", err
	case "checkout":
		if s.billing == nil || o.CheckoutOfferID == "" {
			return "", flow.ErrUnavailable
		}
		session, err := s.billing.Checkout(ctx, actor, billing.CheckoutRequest{OrganizationID: org, OfferID: o.CheckoutOfferID, IdempotencyKey: fmt.Sprintf("onboard_%s_%d", org, revision)})
		if err != nil {
			return "", err
		}
		return session.URL, nil
	default:
		return "", flow.ErrInvalid
	}
}
func (s *OnboardingService) Repositories(ctx context.Context, actor authorization.Principal, org string, installation int64) ([]flow.Repository, error) {
	if installation <= 0 {
		return nil, flow.ErrInvalid
	}
	if err := s.authorize(ctx, actor, org, authorization.KeyOrganizationManage); err != nil {
		return nil, err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return nil, err
	}
	if err = s.ready(ctx, v); err != nil {
		return nil, err
	}
	rows, err := s.github.Repositories(ctx, actor, installation)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, flow.ErrInvalid
	}
	seen := map[string]bool{}
	result := []flow.Repository{}
	private, err := s.entitlements.Has(ctx, org, feature.KeyIntegrationGitHubPrivate)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Validate() != nil || seen[r.ID] {
			return nil, flow.ErrInvalid
		}
		seen[r.ID] = true
		if !r.Private || private {
			result = append(result, r)
		}
	}
	return result, nil
}
func (s *OnboardingService) SelectRepositories(ctx context.Context, actor authorization.Principal, org string, revision, installation int64, ids []string) (flow.State, error) {
	if len(ids) == 0 || len(ids) > 50 {
		return flow.State{}, flow.ErrInvalid
	}
	rows, err := s.Repositories(ctx, actor, org, installation)
	if err != nil {
		return flow.State{}, err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return flow.State{}, err
	}
	if v.Revision != revision || v.ProofID != "" {
		return flow.State{}, flow.ErrConflict
	}
	available := map[string]flow.Repository{}
	for _, r := range rows {
		available[r.ID] = r
	}
	selected := []flow.Repository{}
	for _, id := range ids {
		r, ok := available[id]
		if !ok {
			return flow.State{}, flow.ErrInvalid
		}
		selected = append(selected, r)
		delete(available, id)
	}
	v.InstallationID = installation
	v.Repositories = selected
	v.PolicyID = ""
	return s.store.Save(ctx, v, revision)
}
func (s *OnboardingService) SelectPolicy(ctx context.Context, actor authorization.Principal, org string, revision int64, id string) (flow.State, error) {
	if err := s.authorize(ctx, actor, org, authorization.KeyPolicyManage); err != nil {
		return flow.State{}, err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return flow.State{}, err
	}
	if v.Revision != revision || v.ProofID != "" {
		return flow.State{}, flow.ErrConflict
	}
	if len(v.Repositories) == 0 {
		return flow.State{}, flow.ErrNotReady
	}
	if err = s.ready(ctx, v); err != nil {
		return flow.State{}, err
	}
	p, ok := s.policies[id]
	if !ok {
		return flow.State{}, flow.ErrInvalid
	}
	has, err := s.entitlements.Has(ctx, org, p.Feature)
	if err != nil {
		return flow.State{}, err
	}
	if !has {
		return flow.State{}, entitlement.ErrNotEntitled
	}
	v.PolicyID = id
	return s.store.Save(ctx, v, revision)
}
func (s *OnboardingService) RunFirstProof(ctx context.Context, actor authorization.Principal, org string, revision int64) (FirstProof, error) {
	if err := s.authorize(ctx, actor, org, authorization.KeyProofExecute); err != nil {
		return FirstProof{}, err
	}
	v, err := s.load(ctx, org)
	if err != nil {
		return FirstProof{}, err
	}
	if v.ProofID != "" {
		return s.proofs.Read(ctx, org, v.ProofID)
	}
	if v.Revision != revision {
		return FirstProof{}, flow.ErrConflict
	}
	if err = s.ready(ctx, v); err != nil {
		return FirstProof{}, err
	}
	p, ok := s.policies[v.PolicyID]
	if !ok || len(v.Repositories) == 0 {
		return FirstProof{}, flow.ErrNotReady
	}
	for _, key := range []feature.Key{feature.KeyVerifyBasic, p.Feature} {
		has, err := s.entitlements.Has(ctx, org, key)
		if err != nil {
			return FirstProof{}, err
		}
		if !has {
			return FirstProof{}, entitlement.ErrNotEntitled
		}
	}
	// Re-read GitHub permissions; stale selections are never worker authority.
	rows, err := s.Repositories(ctx, actor, org, v.InstallationID)
	if err != nil {
		return FirstProof{}, err
	}
	for _, r := range v.Repositories {
		if !slices.Contains(rows, r) {
			return FirstProof{}, flow.ErrNotReady
		}
	}
	return s.proofs.Submit(ctx, actor, v, fmt.Sprintf("first_%s_%d", org, revision))
}
