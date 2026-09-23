// Package onboardinghost composes the shared SaaS onboarding HTTP boundary.
package onboardinghost

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/nats-io/nats.go"
	"io"
	"net/http"
	"os"
	"os/signal"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/billing/stripe"
	"safepatch/internal/config"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/domain/organization"
	"safepatch/internal/domain/trial"
	"safepatch/internal/platform/billingstore"
	"safepatch/internal/platform/downloads"
	"safepatch/internal/platform/githubapp"
	"safepatch/internal/platform/mcpgateway"
	"safepatch/internal/platform/onboardinghttp"
	"safepatch/internal/platform/onboardingstore"
	"safepatch/internal/platform/organizationstore"
	"safepatch/internal/platform/trialstore"
	"strconv"
	"strings"
	"time"
)

func Run(stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := serve(ctx); err != nil {
		fmt.Fprintln(stderr, "onboarding API unavailable: check trusted server configuration and infrastructure")
		return 1
	}
	return 0
}
func serve(ctx context.Context) error {
	origin := os.Getenv("SAFEPATCH_PUBLIC_ORIGIN")
	dsn := os.Getenv("SAFEPATCH_DATABASE_URL")
	broker := os.Getenv("SAFEPATCH_NATS_URL")
	stream := os.Getenv("SAFEPATCH_PROOF_STREAM")
	key, err := base64.StdEncoding.DecodeString(os.Getenv("SAFEPATCH_SESSION_KEY"))
	if err != nil || len(key) != 32 || dsn == "" || broker == "" || stream == "" {
		return flow.ErrInvalid
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	startup, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if err = db.PingContext(startup); err != nil {
		return err
	}
	nc, err := nats.Connect(broker, nats.Timeout(10*time.Second), nats.Name("safepatch-onboarding"))
	if err != nil {
		return err
	}
	defer nc.Close()
	publisher, err := onboardingstore.NewJetStreamPublisher(nc, stream)
	if err != nil {
		return err
	}
	features, err := feature.NewBuiltinCatalog("0.2.0", time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		return err
	}
	matrix, err := config.V02Editions(features)
	if err != nil {
		return err
	}
	catalog, _ := edition.NewCatalog(features)
	versions := map[edition.Key]string{}
	for _, e := range matrix.Editions {
		catalog, err = catalog.Publish(e.Bundle.Version, e.Bundle.Features)
		if err != nil {
			return err
		}
		versions[e.Bundle.Version.EditionKey] = e.Bundle.Version.EditionVersionID
	}
	days := 0
	if raw := os.Getenv("SAFEPATCH_BUSINESS_TRIAL_DAYS"); raw != "" {
		days, err = strconv.Atoi(raw)
		if err != nil {
			return err
		}
	}
	business := ""
	if days != 0 {
		business = versions[edition.Business]
	}
	policy, err := trial.NewPolicy(versions[edition.Developer], versions[edition.Team], business, days)
	if err != nil {
		return err
	}
	organizations, err := organizationstore.NewPostgres(db, features)
	if err != nil {
		return err
	}
	permissions, err := authorization.NewPermissionService(organizations)
	if err != nil {
		return err
	}
	runtime, err := billingstore.NewOrganizationRuntime(db, features, versions[edition.Community], time.Now)
	if err != nil {
		return err
	}
	trials, err := trialstore.NewManagement(db, policy, features, time.Now)
	if err != nil {
		return err
	}
	store, err := onboardingstore.New(db, time.Now)
	if err != nil {
		return err
	}
	proofs, err := onboardingstore.NewProofs(store, publisher)
	if err != nil {
		return err
	}
	sessions, err := onboardinghttp.NewSessions(db, key, time.Now)
	if err != nil {
		return err
	}
	github, err := onboardinghttp.NewGitHub(onboardinghttp.GitHubConfig{ClientID: os.Getenv("SAFEPATCH_GITHUB_CLIENT_ID"), ClientSecret: os.Getenv("SAFEPATCH_GITHUB_CLIENT_SECRET"), AppSlug: os.Getenv("SAFEPATCH_GITHUB_APP_SLUG"), CallbackURL: origin + "/v1/auth/github/callback"}, sessions)
	if err != nil {
		return err
	}
	var checkout application.OnboardingBilling
	var webhook http.Handler
	offerIDs := map[edition.Key]string{}
	if apiKey := os.Getenv("SAFEPATCH_STRIPE_API_KEY"); apiKey != "" {
		prices := map[edition.Key]string{}
		if json.Unmarshal([]byte(os.Getenv("SAFEPATCH_STRIPE_PRICES")), &prices) != nil {
			return flow.ErrInvalid
		}
		offers := []stripe.Offer{}
		for _, e := range []edition.Key{edition.Developer, edition.Team, edition.Business} {
			price := prices[e]
			if price == "" {
				return flow.ErrInvalid
			}
			id := "onboarding_" + string(e)
			offerIDs[e] = id
			offers = append(offers, stripe.Offer{OfferID: id, EditionVersionID: versions[e], PriceID: price})
		}
		connection := billingstore.Connection{ID: os.Getenv("SAFEPATCH_STRIPE_CONNECTION_ID"), Provider: stripe.Provider, AccountReference: os.Getenv("SAFEPATCH_STRIPE_ACCOUNT_ID"), LiveMode: strings.Contains(apiKey, "_live_")}
		subscriptions, err := billingstore.NewStripeSubscriptions(db, connection, stripe.Config{APIKey: apiKey, WebhookSecrets: []string{os.Getenv("SAFEPATCH_STRIPE_WEBHOOK_SECRET")}, LiveMode: connection.LiveMode, Offers: offers, Catalog: catalog, SuccessURL: origin + "/onboarding", CancelURL: origin + "/onboarding", PortalReturnURL: origin + "/onboarding"}, features, versions[edition.Community], time.Now)
		if err != nil {
			return err
		}
		// Connection registration is trusted provisioning. Production should register
		// the account once before starting; startup does not silently change it.
		checkout = &billingstore.OnboardingCheckout{Subscriptions: subscriptions, Permissions: permissions}
		webhook = subscriptions.Webhook
	}
	offers := []application.OnboardingOffer{}
	for _, e := range edition.Editions() {
		offers = append(offers, application.OnboardingOffer{Edition: e.Key, VersionID: versions[e.Key], CheckoutOfferID: offerIDs[e.Key]})
	}
	service, err := application.NewOnboardingService(store, organizations, permissions, runtime, github, checkout, trials, proofs, offers, config.OnboardingPolicies())
	if err != nil {
		return err
	}
	handler, err := onboardinghttp.NewHandler(sessions, github, service, organizations, origin)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/v1/auth/", handler)
	mux.Handle("/v1/onboarding/", handler)
	if path := os.Getenv("SAFEPATCH_DOWNLOADS_CONFIG"); path != "" {
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		raw, e := io.ReadAll(io.LimitReader(file, 2*1024*1024+1))
		_ = file.Close()
		if e != nil || len(raw) > 2*1024*1024 {
			return flow.ErrInvalid
		}
		h, e := downloads.NewConfigured(ctx, raw, application.PlatformAccess{Tenants: organizations, Entitlements: runtime, Permissions: permissions, Quotas: downloads.Unmetered{}}, sessions)
		if e != nil {
			return e
		}
		h.Register(mux)
	}
	if path := os.Getenv("SAFEPATCH_AGENT_CONFIG"); path != "" {
		file, e := os.Open(path)
		if e != nil {
			return e
		}
		raw, e := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		_ = file.Close()
		if e != nil || len(raw) > 1024*1024 {
			return flow.ErrInvalid
		}
		agentHandler, e := mcpgateway.NewConfigured(raw, db, organizations, runtime, permissions, sessions, os.Getenv)
		if e != nil {
			return e
		}
		agentHandler.Register(mux)
	}
	var flushPR func(context.Context) error
	if secret := os.Getenv("SAFEPATCH_GITHUB_WEBHOOK_SECRET"); secret != "" {
		prStore, e := githubapp.NewStore(db, time.Now)
		if e != nil {
			return e
		}
		admission, e := application.NewPullRequestAdmission(prStore, organizations, runtime, permissions)
		if e != nil {
			return e
		}
		prWebhook, e := githubapp.NewWebhook([]string{secret}, admission)
		if e != nil {
			return e
		}
		prStream := os.Getenv("SAFEPATCH_GITHUB_PR_STREAM")
		if !organization.ValidID(prStream) {
			return flow.ErrInvalid
		}
		mux.Handle("/v1/integrations/github/webhook", prWebhook)
		flushPR = func(c context.Context) error { return prStore.Flush(c, nc, prStream) }
	}
	if webhook != nil {
		mux.Handle("/v1/billing/stripe/webhook", webhook)
	}
	addr := os.Getenv("SAFEPATCH_API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				work, cancel := context.WithTimeout(ctx, 15*time.Second)
				_ = proofs.Flush(work)
				if flushPR != nil {
					_ = flushPR(work)
				}
				_ = sessions.Cleanup(work)
				cancel()
			}
		}
	}()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
