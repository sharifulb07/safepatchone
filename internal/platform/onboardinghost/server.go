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
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/billing/paddle"
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
	"sync/atomic"
	"syscall"
	"time"
)

func Run(stderr io.Writer) int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
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
	webhookPath := "/v1/billing/stripe/webhook"
	offerIDs := map[edition.Key]string{}
	provider, err := billingProvider(os.Getenv)
	if err != nil {
		return err
	}
	if provider == "stripe" {
		apiKey := os.Getenv("SAFEPATCH_STRIPE_API_KEY")
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
	if provider == "paddle" {
		var prices map[edition.Key]string
		if json.Unmarshal([]byte(os.Getenv("SAFEPATCH_PADDLE_PRICES")), &prices) != nil || len(prices) != 3 {
			return flow.ErrInvalid
		}
		var offers []paddle.Offer
		for _, e := range []edition.Key{edition.Developer, edition.Team, edition.Business} {
			if prices[e] == "" {
				return flow.ErrInvalid
			}
			id := "onboarding_" + string(e)
			offerIDs[e] = id
			offers = append(offers, paddle.Offer{OfferID: id, EditionVersionID: versions[e], PriceID: prices[e]})
		}
		environment := os.Getenv("SAFEPATCH_PADDLE_ENVIRONMENT")
		connection := billingstore.Connection{ID: os.Getenv("SAFEPATCH_PADDLE_CONNECTION_ID"), Provider: paddle.Provider, AccountReference: os.Getenv("SAFEPATCH_PADDLE_ACCOUNT_ID"), LiveMode: environment == "production"}
		subscriptions, err := billingstore.NewPaddleSubscriptions(db, connection, paddle.Config{APIKey: os.Getenv("SAFEPATCH_PADDLE_API_KEY"), WebhookSecrets: []string{os.Getenv("SAFEPATCH_PADDLE_WEBHOOK_SECRET")}, Environment: environment, Offers: offers, Catalog: catalog, CheckoutURL: origin + "/checkout"}, features, time.Now)
		if err != nil {
			return err
		}
		// Fail startup when the immutable account/environment binding was not provisioned.
		var bound bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM billing_connections WHERE connection_id=$1 AND provider='paddle' AND account_reference=$2 AND live_mode=$3)`, connection.ID, connection.AccountReference, connection.LiveMode).Scan(&bound); err != nil || !bound {
			return flow.ErrInvalid
		}
		checkout, webhook = subscriptions, subscriptions.Webhook
		webhookPath = "/v1/billing/paddle/webhook"
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
		downloadMux := http.NewServeMux()
		h.Register(downloadMux)
		browserDownloads, e := onboardinghttp.BrowserDownloads(downloadMux, sessions, origin)
		if e != nil {
			return e
		}
		mux.Handle("/v1/downloads/", downloadMux)
		mux.Handle("/v1/organizations/", browserDownloads)
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
		mux.Handle(webhookPath, webhook)
	}
	addr := os.Getenv("SAFEPATCH_API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16384}
	var backgroundFailed atomic.Bool
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		check, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if ctx.Err() != nil || backgroundFailed.Load() || !nc.IsConnected() || db.PingContext(check) != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	// Registered after DB/broker cleanup so maintenance stops before either
	// dependency closes, including when listener startup fails.
	stopMaintenance := startMaintenance(ctx, 5*time.Second, func(work context.Context) {
		failed := false
		if err := proofs.Flush(work); err != nil {
			failed = true
			slog.Error("proof outbox flush failed")
		}
		if flushPR != nil {
			if err := flushPR(work); err != nil {
				failed = true
				slog.Error("pull request outbox flush failed")
			}
		}
		if err := sessions.Cleanup(work); err != nil {
			failed = true
			slog.Error("session cleanup failed")
		}
		backgroundFailed.Store(failed)
	})
	defer stopMaintenance()
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	return serveHTTP(ctx, server, listener)
}

// The returned stop function cancels outstanding maintenance and waits for it
// to release its resources. Work must honor its bounded context.
func startMaintenance(parent context.Context, interval time.Duration, work func(context.Context)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				bounded, stop := context.WithTimeout(ctx, 15*time.Second)
				work(bounded)
				stop()
			}
		}
	}()
	return func() { cancel(); <-done }
}

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener) error {
	stopped := make(chan struct{})
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		select {
		case <-ctx.Done():
		case <-stopped:
			return
		}
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	}()
	err := server.Serve(listener)
	close(stopped)
	// Shutdown closes the listener before requests have drained. Keep DB and
	// broker resources alive until it actually finishes, including on SIGTERM.
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
