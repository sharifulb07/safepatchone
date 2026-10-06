package onboardinghttp

import (
	"context"
	"net/http"
	"net/url"
	"time"

	flow "safepatch/internal/domain/onboarding"
)

// BrowserDownloads bridges the HttpOnly browser session to the bearer-only
// download boundary after authenticating it and checking mutation CSRF. CLI
// bearer requests remain supported without granting authority to browser claims.
func BrowserDownloads(next http.Handler, sessions *Sessions, origin string) (http.Handler, error) {
	u, err := url.Parse(origin)
	if next == nil || sessions == nil || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, flow.ErrInvalid
	}
	return browserDownloads(next, sessions, origin), nil
}

func browserDownloads(next http.Handler, sessions authSessions, origin string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		w.Header().Set("Cache-Control", "private, no-store")
		session := token(r, sessionCookie)
		if len(r.CookiesNamed(sessionCookie)) > 0 {
			if len(r.Header.Values("Authorization")) != 0 {
				output(w, 400, map[string]string{"error": "AMBIGUOUS_AUTHENTICATION"})
				return
			}
			if session == "" {
				output(w, 401, map[string]string{"error": "AUTHENTICATION_REQUIRED"})
				return
			}
			mutating := r.Method != "GET" && r.Method != "HEAD"
			if mutating && r.Header.Get("Origin") != origin {
				output(w, 403, map[string]string{"error": "ORIGIN_REJECTED"})
				return
			}
			if _, err := sessions.authenticate(r.Context(), session, r.Header.Get("X-CSRF-Token"), mutating); err != nil {
				output(w, 401, map[string]string{"error": "AUTHENTICATION_REQUIRED"})
				return
			}
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+session)
		}
		next.ServeHTTP(w, r)
	})
}
