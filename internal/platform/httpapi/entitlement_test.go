package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/httpapi"
	"safepatch/internal/tenant"
)

type apiFixture struct {
	stop  string
	calls atomic.Int64
}

func (f *apiFixture) Authenticate(_ context.Context, token string) (authorization.Principal, error) {
	if token != "trusted" || f.stop == "auth" {
		return authorization.Principal{}, errors.New("secret auth failure")
	}
	return authorization.Principal{ID: "owner", Kind: authorization.User}, nil
}
func (f *apiFixture) Resolve(_ context.Context, _ authorization.Principal, scope tenant.Scope) (tenant.Scope, error) {
	if scope.OrganizationID != "org_a" || scope.ResourceID != "repo_a" || f.stop == "tenant" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return scope, nil
}

type apiOwnership struct{ fixture *apiFixture }

func (s apiOwnership) Has(_ context.Context, org string, key feature.Key) (bool, error) {
	s.fixture.calls.Add(1)
	if org != "org_a" || key != feature.KeyRuntimeReplay {
		return false, errors.New("wrong bound scope or key")
	}
	if s.fixture.stop == "backend" {
		return true, errors.New("secret scanner token")
	}
	return s.fixture.stop != "entitlement", nil
}
func (apiOwnership) Limit(context.Context, string, feature.Key) (int64, error) { panic("unused") }
func (apiOwnership) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	panic("unused")
}
func (f *apiFixture) Allowed(context.Context, authorization.Principal, string, authorization.Key) (bool, error) {
	return f.stop != "permission", nil
}
func (f *apiFixture) Check(context.Context, application.Execution) error {
	if f.stop == "quota" {
		return entitlement.ErrQuotaExceeded
	}
	return nil
}

func config(f *apiFixture) httpapi.EnforcementConfig {
	return httpapi.EnforcementConfig{Authenticator: f, Tenants: f, Entitlements: apiOwnership{f}, Permissions: f, Quotas: f, Permission: authorization.KeyReplayExecute, Request: func(r *http.Request) (application.Request, error) {
		if f.stop == "decode" {
			return application.Request{}, errors.New("secret request payload")
		}
		return application.Request{Credential: r.Header.Get("Authorization"), Target: tenant.Scope{OrganizationID: "org_a", ResourceID: "repo_a"}}, nil
	}}
}

func TestAPIEnforcesDirectRequestsAndSanitizesErrors(t *testing.T) {
	for _, tc := range []struct {
		stop   string
		status int
		code   string
	}{
		{"success", 204, ""}, {"auth", 401, "AUTHENTICATION_REQUIRED"}, {"tenant", 403, "TENANT_ACCESS_DENIED"},
		{"entitlement", 403, "FEATURE_NOT_ENTITLED"}, {"permission", 403, "PERMISSION_DENIED"}, {"quota", 429, "QUOTA_DENIED"},
		{"backend", 503, "ENFORCEMENT_UNAVAILABLE"}, {"decode", 400, "INVALID_REQUEST"},
	} {
		t.Run(tc.stop, func(t *testing.T) {
			fixture := &apiFixture{stop: tc.stop}
			middleware, err := httpapi.NewEntitlementMiddleware(config(fixture))
			if err != nil {
				t.Fatal(err)
			}
			wrap, err := middleware.RequireEntitlement("runtime.replay")
			if err != nil {
				t.Fatal(err)
			}
			runs := 0
			handler := wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				runs++
				execution, ok := httpapi.ExecutionFromContext(r.Context())
				if !ok || execution.Requirement.Feature != feature.KeyRuntimeReplay || execution.Requirement.Permission != authorization.KeyReplayExecute || execution.Scope.OrganizationID != "org_a" {
					t.Fatal("missing trusted scope", execution)
				}
				w.WriteHeader(204)
			}))
			req := httptest.NewRequest("POST", "/replay?feature=verify.basic&entitled=true", strings.NewReader(`{"plan":"enterprise","permission":"owner","approved":true}`))
			req.Header.Set("Authorization", "trusted")
			req.Header.Set("X-Entitled", "true")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.status || (runs == 1) != (tc.status == 204) {
				t.Fatal(response.Code, response.Body.String(), runs)
			}
			if tc.status != 204 {
				var body struct {
					Error struct {
						Code    string `json:"code"`
						Feature string `json:"feature"`
					} `json:"error"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != tc.code || body.Error.Feature != "runtime.replay" {
					t.Fatal(err, response.Body.String())
				}
				if strings.Contains(response.Body.String(), "secret") || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" {
					t.Fatal(response.Body.String())
				}
			}
		})
	}
}

func TestMiddlewareConcurrentCallsDoNotReuseAdmission(t *testing.T) {
	f := &apiFixture{stop: "success"}
	m, _ := httpapi.NewEntitlementMiddleware(config(f))
	wrap, _ := m.RequireEntitlement("runtime.replay")
	var runs atomic.Int64
	handler := wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { runs.Add(1); w.WriteHeader(204) }))
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/replay", nil)
			req.Header.Set("Authorization", "trusted")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != 204 {
				t.Error(recorder.Code)
			}
		}()
	}
	wg.Wait()
	if f.calls.Load() != 20 || runs.Load() != 20 {
		t.Fatal("checks were cached", f.calls.Load(), runs.Load())
	}
	f.stop = "entitlement"
	req := httptest.NewRequest("POST", "/replay", nil)
	req.Header.Set("Authorization", "trusted")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != 403 || runs.Load() != 20 {
		t.Fatal("revocation bypassed", recorder.Code)
	}
}

func TestMiddlewareInvalidConfigurationAndCancelledRequest(t *testing.T) {
	if _, err := httpapi.NewEntitlementMiddleware(httpapi.EnforcementConfig{}); !errors.Is(err, application.ErrInvalidGuard) {
		t.Fatal(err)
	}
	var zero httpapi.EntitlementMiddleware
	if _, err := zero.RequireEntitlement("runtime.replay"); err == nil {
		t.Fatal("zero middleware accepted")
	}
	f := &apiFixture{}
	m, _ := httpapi.NewEntitlementMiddleware(config(f))
	if _, err := m.RequireEntitlement("../runtime.replay"); err == nil {
		t.Fatal("invalid feature bound")
	}
	wrap, _ := m.RequireEntitlement("runtime.replay")
	req := httptest.NewRequest("POST", "/replay", nil)
	req.Header.Set("Authorization", "trusted")
	ctx, cancel := context.WithCancel(req.Context())
	cancel()
	response := httptest.NewRecorder()
	wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("cancelled workload started") })).ServeHTTP(response, req.WithContext(ctx))
	if response.Code != 503 || f.calls.Load() != 0 {
		t.Fatal(response.Code, f.calls.Load())
	}
	response = httptest.NewRecorder()
	wrap(nil).ServeHTTP(response, req)
	if response.Code != 503 {
		t.Fatal("nil handler permitted", response.Code)
	}
	if _, ok := httpapi.ExecutionFromContext(context.Background()); ok {
		t.Fatal("unauthorized context")
	}
}
