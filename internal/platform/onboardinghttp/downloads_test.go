package onboardinghttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserDownloadsRequiresSessionOriginAndCSRF(t *testing.T) {
	for _, mode := range []string{"valid", "cross_origin", "missing_csrf", "invalid_session", "ambiguous", "duplicate_cookie", "empty_cookie", "empty_cookie_bearer", "catalog", "bearer"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			h := browserDownloads(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer opaque" {
					t.Fatal("missing server session")
				}
				w.WriteHeader(204)
			}), &testSessions{}, "https://safepatch.example")
			r := httptest.NewRequest("POST", "/v1/organizations/org/downloads/host", nil)
			r.Header.Set("Origin", "https://safepatch.example")
			r.Header.Set("X-CSRF-Token", "csrf")
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "opaque"})
			switch mode {
			case "cross_origin":
				r.Header.Set("Origin", "https://attacker.example")
			case "missing_csrf":
				r.Header.Del("X-CSRF-Token")
			case "invalid_session":
				r.Header.Set("Cookie", sessionCookie+"=forged")
			case "ambiguous":
				r.Header.Set("Authorization", "Bearer opaque")
			case "duplicate_cookie":
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})
			case "empty_cookie", "empty_cookie_bearer":
				r.Header.Set("Cookie", sessionCookie+"=")
				if mode == "empty_cookie_bearer" {
					r.Header.Set("Authorization", "Bearer opaque")
				}
			case "catalog":
				r.Method = "GET"
				r.Header.Del("X-CSRF-Token")
			case "bearer":
				r.Header.Del("Cookie")
				r.Header.Set("Authorization", "Bearer opaque")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			valid := mode == "valid" || mode == "catalog" || mode == "bearer"
			if valid && (w.Code != 204 || calls != 1) || !valid && (w.Code < 400 || calls != 0) {
				t.Fatalf("%d calls=%d", w.Code, calls)
			}
		})
	}
}
