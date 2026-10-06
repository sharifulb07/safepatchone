package onboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/jsonobject"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/organizationstore"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "__Host-safepatch_session"
const csrfCookie = "__Host-safepatch_csrf"
const oauthCookie = "__Host-safepatch_oauth"
const returnCookie = "__Host-safepatch_return"

// Only local onboarding preferences can survive OAuth. Neither a destination
// nor an edition selection grants an entitlement or authorizes a mutation.
func loginDestination(raw string) string {
	if raw == "" {
		return "/onboarding"
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" {
		return ""
	}
	if u.Path == "/downloads" && u.RawQuery == "" {
		return "/downloads"
	}
	if u.Path != "/onboarding" {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) > 1 {
		return ""
	}
	if len(query) == 0 {
		return "/onboarding"
	}
	values := query["edition"]
	if len(values) != 1 {
		return ""
	}
	if _, err := edition.Lookup(edition.Key(values[0])); err != nil {
		return ""
	}
	return "/onboarding?" + url.Values{"edition": values}.Encode()
}

type authSessions interface {
	begin(context.Context) (string, string, string, error)
	consume(context.Context, string, string) (string, error)
	authenticate(context.Context, string, string, bool) (authorization.Principal, error)
	logout(context.Context, string) error
}
type Handler struct {
	sessions      authSessions
	github        *GitHub
	service       *application.OnboardingService
	organizations *organizationstore.Postgres
	origin        string
	mu            sync.Mutex
	limits        map[string]window
}
type window struct {
	at    time.Time
	count int
}

func NewHandler(s *Sessions, g *GitHub, service *application.OnboardingService, organizations *organizationstore.Postgres, publicOrigin string) (http.Handler, error) {
	u, err := url.Parse(publicOrigin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || s == nil || g == nil || service == nil || organizations == nil {
		return nil, flow.ErrInvalid
	}
	return &Handler{sessions: s, github: g, service: service, organizations: organizations, origin: publicOrigin, limits: map[string]window{}}, nil
}
func (h *Handler) allowedIP(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for k, v := range h.limits {
		if now.Sub(v.at) > time.Minute {
			delete(h.limits, k)
		}
	}
	v := h.limits[host]
	if v.at.IsZero() {
		if len(h.limits) >= 4096 {
			return false
		}
		v.at = now
	}
	v.count++
	h.limits[host] = v
	return v.count <= 120
}
func cookie(w http.ResponseWriter, name, value string, httpOnly bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: true, HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, Expires: expires})
}
func token(r *http.Request, name string) string {
	cookies := r.CookiesNamed(name)
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}
func output(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	code, status := "ONBOARDING_UNAVAILABLE", 503
	switch {
	case errors.Is(err, flow.ErrInvalid):
		code, status = "INVALID_REQUEST", 400
	case errors.Is(err, flow.ErrConflict):
		code, status = "ONBOARDING_CONFLICT", 409
	case errors.Is(err, flow.ErrNotReady):
		code, status = "PREREQUISITE_NOT_READY", 409
	case errors.Is(err, application.ErrPermissionDenied):
		code, status = "PERMISSION_DENIED", 403
	case errors.Is(err, entitlement.ErrNotEntitled):
		code, status = "FEATURE_NOT_ENTITLED", 403
	}
	output(w, status, map[string]string{"error": code})
}
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return flow.ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return flow.ErrInvalid
	}
	checked, err := jsonobject.Normalize(raw, 32768)
	if err != nil {
		return flow.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(checked))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return flow.ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return flow.ErrInvalid
	}
	return nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if !h.allowedIP(r.RemoteAddr) {
		output(w, 429, map[string]string{"error": "RATE_LIMITED"})
		return
	}
	if r.Method != "GET" && r.Method != "POST" {
		w.Header().Set("Allow", "GET, POST")
		output(w, 405, map[string]string{"error": "METHOD_NOT_ALLOWED"})
		return
	}
	if r.URL.Path == "/v1/auth/github/start" && r.Method == "GET" {
		destination := loginDestination(r.URL.Query().Get("return_to"))
		if destination == "" {
			failure(w, flow.ErrInvalid)
			return
		}
		state, browser, challenge, err := h.sessions.begin(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		cookie(w, oauthCookie, browser, true, time.Now().Add(10*time.Minute))
		cookie(w, returnCookie, destination, true, time.Now().Add(10*time.Minute))
		http.Redirect(w, r, h.github.AuthorizeURL(state, challenge), http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/v1/auth/github/callback" && r.Method == "GET" {
		verifier, err := h.sessions.consume(ctx, r.URL.Query().Get("state"), token(r, oauthCookie))
		if err != nil {
			failure(w, flow.ErrInvalid)
			return
		}
		cookie(w, oauthCookie, "", true, time.Unix(1, 0))
		session, csrf, until, err := h.github.Exchange(ctx, r.URL.Query().Get("code"), verifier)
		if err != nil {
			failure(w, err)
			return
		}
		cookie(w, sessionCookie, session, true, until)
		cookie(w, csrfCookie, csrf, false, until)
		destination := loginDestination(token(r, returnCookie))
		if destination == "" {
			destination = "/onboarding"
		}
		cookie(w, returnCookie, "", true, time.Unix(1, 0))
		http.Redirect(w, r, h.origin+destination, http.StatusSeeOther)
		return
	}
	mutating := r.Method == "POST"
	if mutating && r.Header.Get("Origin") != h.origin {
		output(w, 403, map[string]string{"error": "ORIGIN_REJECTED"})
		return
	}
	actor, err := h.sessions.authenticate(ctx, token(r, sessionCookie), r.Header.Get("X-CSRF-Token"), mutating)
	if err != nil {
		output(w, 401, map[string]string{"error": "AUTHENTICATION_REQUIRED"})
		return
	}
	if r.URL.Path == "/v1/onboarding/session" && r.Method == "GET" {
		orgs, err := h.organizations.ListOrganizations(ctx, actor, "", 100)
		if err != nil {
			failure(w, err)
			return
		}
		output(w, 200, map[string]any{"user_id": actor.ID, "organizations": orgs.Organizations, "github_install_url": h.github.InstallURL()})
		return
	}
	if r.URL.Path == "/v1/onboarding/logout" && mutating {
		if err = h.sessions.logout(ctx, token(r, sessionCookie)); err != nil {
			failure(w, err)
			return
		}
		cookie(w, sessionCookie, "", true, time.Unix(1, 0))
		cookie(w, csrfCookie, "", false, time.Unix(1, 0))
		output(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/v1/onboarding/installations" && r.Method == "GET" {
		rows, err := h.github.Installations(ctx, actor)
		if err != nil {
			failure(w, err)
			return
		}
		output(w, 200, rows)
		return
	}
	if r.URL.Path == "/v1/onboarding/organizations" && mutating {
		var body struct {
			Attempt string `json:"attempt"`
			Name    string `json:"name"`
		}
		if err = readBody(w, r, &body); err != nil {
			failure(w, err)
			return
		}
		v, err := h.service.Create(ctx, actor, body.Attempt, body.Name)
		if err != nil {
			failure(w, err)
			return
		}
		output(w, 201, v)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/onboarding/"), "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	org := parts[0]
	if len(parts) == 1 && r.Method == "GET" {
		v, err := h.service.View(ctx, actor, org)
		if err != nil {
			failure(w, err)
			return
		}
		output(w, 200, v)
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	if parts[1] == "repositories" && r.Method == "GET" {
		id, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
		if err != nil {
			failure(w, flow.ErrInvalid)
			return
		}
		v, err := h.service.Repositories(ctx, actor, org, id)
		if err != nil {
			failure(w, err)
			return
		}
		output(w, 200, v)
		return
	}
	if !mutating {
		http.NotFound(w, r)
		return
	}
	// One bounded request DTO; unknown fields (including success/subscription state)
	// are rejected. Only route-specific fields are ever passed to the service.
	var body struct {
		Revision       int64       `json:"revision"`
		Edition        edition.Key `json:"edition"`
		Mode           string      `json:"mode"`
		InstallationID int64       `json:"installation_id"`
		RepositoryIDs  []string    `json:"repository_ids"`
		PolicyID       string      `json:"policy_id"`
	}
	if err = readBody(w, r, &body); err != nil {
		failure(w, err)
		return
	}
	var result any
	switch parts[1] {
	case "edition":
		result, err = h.service.SelectEdition(ctx, actor, org, body.Revision, body.Edition)
	case "billing":
		var target string
		target, err = h.service.StartBilling(ctx, actor, org, body.Revision, body.Mode)
		if err == nil && target != "" {
			u, e := url.Parse(target)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				err = flow.ErrUnavailable
			}
		}
		result = map[string]string{"redirect_url": target}
	case "repositories":
		result, err = h.service.SelectRepositories(ctx, actor, org, body.Revision, body.InstallationID, body.RepositoryIDs)
	case "policy":
		result, err = h.service.SelectPolicy(ctx, actor, org, body.Revision, body.PolicyID)
	case "proof":
		result, err = h.service.RunFirstProof(ctx, actor, org, body.Revision)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	output(w, 200, result)
}
