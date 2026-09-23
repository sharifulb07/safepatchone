package onboardinghttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"safepatch/internal/authorization"
	flow "safepatch/internal/domain/onboarding"
	"strings"
	"testing"
	"time"
)

type testSessions struct {
	calls int
	err   error
}

func (s *testSessions) begin(context.Context) (string, string, string, error) {
	return "state", "browser", "challenge", nil
}
func (s *testSessions) consume(context.Context, string, string) (string, error) {
	return "", flow.ErrInvalid
}
func (s *testSessions) authenticate(_ context.Context, session, csrf string, mutation bool) (authorization.Principal, error) {
	s.calls++
	if s.err != nil {
		return authorization.Principal{}, s.err
	}
	if session != "opaque" || mutation && csrf != "csrf" {
		return authorization.Principal{}, flow.ErrInvalid
	}
	return authorization.Principal{ID: "user", Kind: authorization.User}, nil
}
func (s *testSessions) logout(context.Context, string) error { return nil }
func TestHTTPRejectsCrossOriginMissingSessionAndCSRF(t *testing.T) {
	for _, mode := range []string{"cross_origin", "missing_session", "missing_csrf", "database_error", "valid_logout"} {
		t.Run(mode, func(t *testing.T) {
			s := &testSessions{}
			h := &Handler{sessions: s, origin: "https://safepatch.example", limits: map[string]window{}}
			r := httptest.NewRequest("POST", "https://safepatch.example/v1/onboarding/logout", strings.NewReader(`{}`))
			r.Header.Set("Origin", h.origin)
			r.Header.Set("X-CSRF-Token", "csrf")
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "opaque"})
			switch mode {
			case "cross_origin":
				r.Header.Set("Origin", "https://attacker.example")
			case "missing_session":
				r.Header.Del("Cookie")
			case "missing_csrf":
				r.Header.Del("X-CSRF-Token")
			case "database_error":
				s.err = errors.New("private database credential")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 401
			if mode == "cross_origin" {
				want = 403
			}
			if mode == "valid_logout" {
				want = 200
			}
			if w.Code != want || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if mode == "cross_origin" && s.calls != 0 {
				t.Fatal("cross origin reached auth")
			}
			for _, c := range w.Result().Cookies() {
				if !c.Secure || c.Path != "/" || c.Domain != "" {
					t.Fatal("unsafe cookie", c)
				}
			}
		})
	}
}
func TestOAuthReplayAndInvalidCallbackFailClosed(t *testing.T) {
	h := &Handler{sessions: &testSessions{}, origin: "https://safepatch.example", limits: map[string]window{}}
	r := httptest.NewRequest("GET", "https://safepatch.example/v1/auth/github/callback?code=code&state=forged", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || len(w.Result().Cookies()) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestEncryptedTokensAreBoundToIdentityAndTamperEvident(t *testing.T) {
	s, err := NewSessions(&sql.DB{}, make([]byte, 32), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	raw := s.seal("secret-provider-token", "user_a")
	if strings.Contains(string(raw), "secret-provider-token") {
		t.Fatal("plaintext token")
	}
	if v, err := s.open(raw, "user_a"); err != nil || v != "secret-provider-token" {
		t.Fatal(err)
	}
	if _, err := s.open(raw, "user_b"); err == nil {
		t.Fatal("cross-user token swap")
	}
	raw[len(raw)-1] ^= 1
	if _, err := s.open(raw, "user_a"); err == nil {
		t.Fatal("tampered token")
	}
}
func TestRequestDecoderRejectsClaimsDuplicatesAndLargeBodies(t *testing.T) {
	for _, body := range []string{`{"revision":1,"revision":2}`, `{"revision":1,"subscription_state":"ACTIVE"}`, `{"revision":1} {}`, strings.Repeat(" ", 32769) + `{}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var v struct {
			Revision int64 `json:"revision"`
		}
		if readBody(httptest.NewRecorder(), r, &v) == nil {
			t.Fatal("accepted untrusted claims")
		}
	}
}
