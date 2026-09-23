package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/trust"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func apiReply(code int, v any) *http.Response {
	raw, _ := json.Marshal(v)
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}
}
func TestAppTokenAndChecksBindInstallationRepositoryAndHead(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	for _, mode := range []string{"pass", "unknown", "missing_evidence", "stale_head", "duplicate", "foreign_repository", "redirect", "poisoned_json"} {
		t.Run(mode, func(t *testing.T) {
			p := application.PullRequest{InstallationID: 7, RepositoryID: 1, Number: 2, BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
			j := application.PullRequestJob{ID: "pr_test", Binding: application.PullRequestBinding{OrganizationID: "org", RepositoryID: "gh_1", Actor: authorization.Principal{ID: "actor", Kind: authorization.User}}, Request: p}
			now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
			writes := 0
			c, err := NewClient(ClientConfig{AppID: 42, PrivateKeyPEM: encoded, Now: func() time.Time { return now }, Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
					t.Fatal("SSRF destination", r.URL)
				}
				switch {
				case r.URL.Path == "/app/installations/7/access_tokens":
					jwt := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
					if len(jwt) != 3 {
						t.Fatal("not JWT")
					}
					sig, _ := base64.RawURLEncoding.DecodeString(jwt[2])
					hash := sha256.Sum256([]byte(jwt[0] + "." + jwt[1]))
					if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig) != nil {
						t.Fatal("invalid signed app token")
					}
					var body struct {
						IDs         []int64           `json:"repository_ids"`
						Permissions map[string]string `json:"permissions"`
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.IDs) != 1 || body.IDs[0] != 1 || body.Permissions["contents"] != "read" || body.Permissions["checks"] != "write" {
						t.Fatal("token not scoped")
					}
					return apiReply(201, map[string]any{"token": "fixture-installation-token", "expires_at": now.Add(time.Hour)}), nil
				case r.URL.Path == "/repositories/1":
					if mode == "redirect" {
						v := apiReply(302, map[string]string{})
						v.Header.Set("Location", "http://169.254.169.254/")
						return v, nil
					}
					id := 1
					if mode == "foreign_repository" {
						id = 2
					}
					return apiReply(200, map[string]any{"id": id, "full_name": "owner/repo", "private": false}), nil
				case r.URL.Path == "/repos/owner/repo/pulls/2":
					head := p.HeadSHA
					if mode == "stale_head" {
						head = strings.Repeat("c", 40)
					}
					return apiReply(200, map[string]any{"number": 2, "state": "open", "base": map[string]any{"sha": p.BaseSHA, "repo": map[string]int{"id": 1}}, "head": map[string]string{"sha": head}}), nil
				case strings.Contains(r.URL.Path, "/commits/"):
					if mode == "poisoned_json" {
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"total_count":0,"total_count":1,"check_runs":[]}`))}, nil
					}
					checks := []map[string]any{}
					if mode == "duplicate" {
						checks = append(checks, map[string]any{"id": 9, "external_id": j.ID, "head_sha": p.HeadSHA, "app": map[string]int{"id": 42}})
					}
					return apiReply(200, map[string]any{"total_count": len(checks), "check_runs": checks}), nil
				case strings.Contains(r.URL.Path, "/check-runs"):
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid check")
					}
					want := "success"
					if mode == "unknown" || mode == "missing_evidence" {
						want = "action_required"
					}
					if mode == "stale_head" {
						want = "cancelled"
					}
					if body["conclusion"] != want || body["external_id"] != j.ID {
						t.Fatal(body)
					}
					if mode == "duplicate" {
						if r.Method != "PATCH" || r.URL.Path != "/repos/owner/repo/check-runs/9" {
							t.Fatal("duplicate check created")
						}
					} else if body["head_sha"] != p.HeadSHA {
						t.Fatal("wrong head")
					}
					return apiReply(201, map[string]any{"id": 9, "external_id": j.ID, "head_sha": p.HeadSHA}), nil
				default:
					t.Fatal("unexpected endpoint", r.URL)
					return nil, application.ErrChangeWorkflow
				}
			})})
			if err != nil {
				t.Fatal(err)
			}
			result := application.ChangeResult{ChangeID: j.Change().ID(), Decision: trust.Pass, EvidenceReference: "immutable"}
			if mode == "unknown" {
				result.Decision = trust.Unknown
			}
			if mode == "missing_evidence" {
				result.EvidenceReference = ""
			}
			err = c.Publish(t.Context(), j, result)
			if mode == "foreign_repository" || mode == "redirect" || mode == "poisoned_json" {
				if err == nil || writes != 0 {
					t.Fatal("unsafe provider data admitted")
				}
			} else if err != nil || writes != 1 {
				t.Fatal(err, writes)
			}
		})
	}
}
