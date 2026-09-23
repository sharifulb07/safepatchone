package application_test

import (
	"context"
	"errors"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/billing"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/trial"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
	"slices"
	"sync"
	"testing"
	"time"
)

type flowStore struct {
	mu    sync.Mutex
	state flow.State
	paid  bool
}

func (s *flowStore) Create(context.Context, string, string, string) (flow.State, error) {
	return s.Load(context.Background(), "org_a")
}
func (s *flowStore) Load(_ context.Context, org string) (flow.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if org != s.state.OrganizationID {
		return flow.State{}, flow.ErrInvalid
	}
	v := s.state
	v.Repositories = slices.Clone(v.Repositories)
	return v, nil
}
func (s *flowStore) Save(_ context.Context, v flow.State, revision int64) (flow.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return flow.State{}, flow.ErrConflict
	}
	v.Revision++
	s.state = v
	return v, nil
}
func (s *flowStore) Access(_ context.Context, org, version string) (flow.Access, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return flow.Access{Ready: s.paid && org == s.state.OrganizationID && version == s.state.EditionVersionID, Source: "SUBSCRIPTION"}, nil
}

type flowEntitlements struct{ enabled map[feature.Key]bool }

func (e flowEntitlements) Has(_ context.Context, _ string, key feature.Key) (bool, error) {
	return e.enabled[key], nil
}
func (e flowEntitlements) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, entitlement.ErrLimitUnspecified
}
func (e flowEntitlements) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, nil
}

type flowGitHub struct {
	calls int
	rows  []flow.Repository
}

func (g *flowGitHub) Repositories(context.Context, authorization.Principal, int64) ([]flow.Repository, error) {
	g.calls++
	return slices.Clone(g.rows), nil
}

type flowBilling struct{ calls int }

func (b *flowBilling) Checkout(context.Context, authorization.Principal, billing.CheckoutRequest) (billing.Session, error) {
	b.calls++
	return billing.Session{URL: "https://checkout.stripe.com/test"}, nil
}

type flowTrials struct{}

func (flowTrials) Start(_ context.Context, _ authorization.Principal, org string, key edition.Key) (trial.Trial, error) {
	p, _ := trial.NewPolicy("developer-2026-v2", "team-2026-v2", "", 0)
	offer, err := p.Offer(key)
	if err != nil {
		return trial.Trial{}, err
	}
	return trial.New(org, offer, time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
}

type flowProofs struct{ calls int }

func (p *flowProofs) Submit(context.Context, authorization.Principal, flow.State, string) (application.FirstProof, error) {
	p.calls++
	return application.FirstProof{ID: "proof", Status: "QUEUED"}, nil
}
func (p *flowProofs) Read(context.Context, string, string) (application.FirstProof, error) {
	return application.FirstProof{ID: "proof", Status: "UNKNOWN"}, nil
}
func newFlow(t *testing.T) (*application.OnboardingService, *flowStore, *flowGitHub, *flowBilling, *flowProofs, flowEntitlements) {
	t.Helper()
	store := &flowStore{state: flow.State{OrganizationID: "org_a", Revision: 1, Repositories: []flow.Repository{}}}
	g := &flowGitHub{rows: []flow.Repository{{ID: "gh_1", FullName: "owner/public"}, {ID: "gh_2", FullName: "owner/private", Private: true}}}
	b := &flowBilling{}
	p := &flowProofs{}
	e := flowEntitlements{map[feature.Key]bool{feature.KeyVerifyBasic: true, feature.KeyPolicyLocalBasic: true}}
	permissions, _ := authorization.NewPermissionService(billingMemberships{active: true})
	offers := []application.OnboardingOffer{}
	for _, ed := range edition.Editions() {
		offers = append(offers, application.OnboardingOffer{Edition: ed.Key, VersionID: string(ed.Key) + "-2026-v2", CheckoutOfferID: string(ed.Key)})
	}
	s, err := application.NewOnboardingService(store, tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	}), permissions, e, g, b, flowTrials{}, p, offers, []application.OnboardingPolicy{{ID: "baseline-v1", Name: "Baseline", Feature: feature.KeyPolicyLocalBasic}, {ID: "strict-v1", Name: "Strict", Feature: feature.KeyPolicyLocalAdvanced}})
	if err != nil {
		t.Fatal(err)
	}
	return s, store, g, b, p, e
}
func TestOnboardingCheckoutAndSelectionNeverGrantAccess(t *testing.T) {
	s, store, g, b, _, _ := newFlow(t)
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	v, err := s.SelectEdition(t.Context(), actor, "org_a", 1, edition.Team)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartBilling(t.Context(), actor, "org_a", v.Revision, "checkout"); err != nil || b.calls != 1 {
		t.Fatal(err)
	}
	view, err := s.View(t.Context(), actor, "org_a")
	if err != nil || view.Access.Ready || store.paid {
		t.Fatal("checkout manufactured access", view, err)
	}
	if _, err = s.Repositories(t.Context(), actor, "org_a", 123); !errors.Is(err, flow.ErrNotReady) || g.calls != 0 {
		t.Fatal("pending subscription reached GitHub", err)
	}
	if _, err = s.StartBilling(t.Context(), actor, "org_a", v.Revision, "trial"); err != nil {
		t.Fatal(err)
	}
	view, _ = s.View(t.Context(), actor, "org_a")
	if view.Access.Ready {
		t.Fatal("trial response trusted instead of authoritative storage")
	}
}
func TestOnboardingFlowRechecksEntitlementPermissionAndRevocation(t *testing.T) {
	s, store, g, _, proofs, grants := newFlow(t)
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	v, _ := s.SelectEdition(t.Context(), actor, "org_a", 1, edition.Team)
	store.paid = true
	rows, err := s.Repositories(t.Context(), actor, "org_a", 1)
	if err != nil || len(rows) != 1 {
		t.Fatal("private entitlement boundary", rows, err)
	}
	if _, err = s.SelectRepositories(t.Context(), actor, "org_a", v.Revision, 1, []string{"gh_2"}); err == nil {
		t.Fatal("private repo admitted")
	}
	grants.enabled[feature.KeyIntegrationGitHubPrivate] = true
	v, err = s.SelectRepositories(t.Context(), actor, "org_a", v.Revision, 1, []string{"gh_1", "gh_2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SelectPolicy(t.Context(), actor, "org_a", v.Revision, "strict-v1"); !errors.Is(err, entitlement.ErrNotEntitled) {
		t.Fatal(err)
	}
	grants.enabled[feature.KeyPolicyLocalAdvanced] = true
	v, err = s.SelectPolicy(t.Context(), actor, "org_a", v.Revision, "strict-v1")
	if err != nil {
		t.Fatal(err)
	}
	before := slices.Clone(v.Repositories)
	// A viewer in another organization cannot use a successful entitlement check.
	if _, err = s.SelectPolicy(t.Context(), actor, "org_b", v.Revision, "strict-v1"); !errors.Is(err, application.ErrPermissionDenied) {
		t.Fatal(err)
	}
	grants.enabled[feature.KeyPolicyLocalAdvanced] = false
	if _, err = s.RunFirstProof(t.Context(), actor, "org_a", v.Revision); !errors.Is(err, entitlement.ErrNotEntitled) || proofs.calls != 0 {
		t.Fatal("downgrade ignored", err)
	}
	grants.enabled[feature.KeyPolicyLocalAdvanced] = true
	g.rows = g.rows[:1]
	if _, err = s.RunFirstProof(t.Context(), actor, "org_a", v.Revision); !errors.Is(err, flow.ErrNotReady) {
		t.Fatal("GitHub revocation ignored", err)
	}
	g.rows = before
	p, err := s.RunFirstProof(t.Context(), actor, "org_a", v.Revision)
	if err != nil || p.Status != "QUEUED" || proofs.calls != 1 {
		t.Fatal(p, err)
	}
	after, _ := store.Load(t.Context(), "org_a")
	if !slices.Equal(before, after.Repositories) {
		t.Fatal("plan change corrupted repositories")
	}
}
func TestOnboardingConcurrentSelectionUsesCAS(t *testing.T) {
	s, _, _, _, _, _ := newFlow(t)
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	results := make(chan error, 2)
	for _, key := range []edition.Key{edition.Team, edition.Developer} {
		go func() { _, err := s.SelectEdition(t.Context(), actor, "org_a", 1, key); results <- err }()
	}
	success := 0
	for range 2 {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, flow.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
}
