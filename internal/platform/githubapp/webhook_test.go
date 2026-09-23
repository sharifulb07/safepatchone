package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"safepatch/internal/application"
	"strings"
	"testing"
)

const webhookSecret = "fixture-only-not-a-secret-0123456789abcdef"

type accepting struct {
	calls int
	p     application.PullRequest
	err   error
}

func (a *accepting) Accept(_ context.Context, _ string, _ string, p application.PullRequest) error {
	a.calls++
	a.p = p
	return a.err
}
func event() string {
	return `{"action":"opened","number":2,"installation":{"id":7},"repository":{"id":1,"private":true},"pull_request":{"number":2,"state":"open","base":{"sha":"` + strings.Repeat("a", 40) + `","repo":{"id":1}},"head":{"sha":"` + strings.Repeat("b", 40) + `"}},"sender":{"login":"owner","id":999}}`
}
func signature(raw string) string {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(raw))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func TestVerifiedPRWebhookIngress(t *testing.T) {
	for _, mode := range []string{"valid", "tampered", "duplicate_json", "foreign_base", "missing_private", "oversized", "unknown_action", "provider_error", "wrong_method"} {
		t.Run(mode, func(t *testing.T) {
			a := &accepting{}
			h, err := NewWebhook([]string{webhookSecret}, a)
			if err != nil {
				t.Fatal(err)
			}
			raw := event()
			switch mode {
			case "duplicate_json":
				raw = strings.Replace(raw, `"number":2`, `"number":2,"number":3`, 1)
			case "foreign_base":
				raw = strings.Replace(raw, `"repo":{"id":1}`, `"repo":{"id":9}`, 1)
			case "missing_private":
				raw = strings.Replace(raw, `,"private":true`, "", 1)
			case "oversized":
				raw = strings.Repeat("x", MaxWebhookBytes+1)
			case "unknown_action":
				raw = strings.Replace(raw, "opened", "closed", 1)
			case "provider_error":
				a.err = application.ErrChangeWorkflow
			}
			r := httptest.NewRequest("POST", "https://app.example/v1/integrations/github/webhook", strings.NewReader(raw))
			r.Header.Set("X-GitHub-Event", "pull_request")
			r.Header.Set("X-GitHub-Delivery", "delivery")
			r.Header.Set("X-Hub-Signature-256", signature(raw))
			if mode == "tampered" {
				r.Header.Set("X-Hub-Signature-256", signature(raw+"x"))
			}
			if mode == "wrong_method" {
				r.Method = "GET"
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 400
			switch mode {
			case "valid":
				want = 202
			case "oversized":
				want = 413
			case "unknown_action":
				want = 204
			case "provider_error":
				want = 503
			case "wrong_method":
				want = 405
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if mode != "valid" && mode != "provider_error" && a.calls != 0 {
				t.Fatal("unverified event reached domain")
			}
			if mode == "valid" && (a.p.RepositoryID != 1 || !a.p.Private || a.calls != 1) {
				t.Fatal(a.p)
			}
		})
	}
}
