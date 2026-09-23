package downloads_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/release"
	"safepatch/internal/entitlement"
	"safepatch/internal/platform/downloads"
	"safepatch/internal/tenant"
)

type access struct {
	org     string
	grants  map[feature.Key]bool
	allowed bool
	fail    bool
	stages  []string
}

func (a *access) Authenticate(_ context.Context, token string) (authorization.Principal, error) {
	a.stages = append(a.stages, "authenticate")
	if token != "valid" {
		return authorization.Principal{}, errors.New("private session error")
	}
	return authorization.Principal{ID: "owner", Kind: authorization.User}, nil
}

type tenants struct{ a *access }

func (t tenants) Resolve(_ context.Context, p authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	t.a.stages = append(t.a.stages, "tenant")
	if s.OrganizationID != t.a.org || p.ID != "owner" {
		return tenant.Scope{}, tenant.ErrInvalidScope
	}
	return s, nil
}

type grants struct{ a *access }

func (g grants) Has(_ context.Context, org string, k feature.Key) (bool, error) {
	g.a.stages = append(g.a.stages, "entitlement")
	if g.a.fail {
		return false, errors.New("private billing error")
	}
	return org == g.a.org && g.a.grants[k], nil
}
func (grants) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, errors.New("unused")
}
func (grants) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, errors.New("unused")
}
func (a *access) Allowed(_ context.Context, _ authorization.Principal, org string, key authorization.Key) (bool, error) {
	a.stages = append(a.stages, "permission")
	return a.allowed && org == a.org && key == authorization.KeyOrganizationManage, nil
}
func (a *access) Check(context.Context, application.Execution) error { return nil }

type signer struct {
	actual *downloads.S3Signer
	a      *access
	calls  int
}

func (s *signer) Sign(ctx context.Context, a release.Artifact, deadline time.Time) (application.DownloadLink, error) {
	s.calls++
	s.a.stages = append(s.a.stages, "sign")
	return s.actual.Sign(ctx, a, deadline)
}

func artifacts() []release.Artifact {
	var result []release.Artifact
	for i, v := range []struct {
		id      string
		kind    release.ArtifactKind
		channel string
	}{{"cli", release.CLI, "stable"}, {"checksums", release.PublicAsset, "stable"}, {"host", release.SelfHostedBundle, "stable"}, {"airgap", release.AirgapBundle, "stable"}, {"lts", release.PublicAsset, "lts"}, {"host_lts", release.SelfHostedBundle, "lts"}} {
		result = append(result, release.Artifact{ID: v.id, Kind: v.kind, Version: "1.5.2", Channel: v.channel, Filename: v.id + ".zip", SHA256: strings.Repeat(strconv.Itoa(i+1), 64), Bytes: 123, AvailableUntil: time.Now().Add(time.Hour)})
	}
	return result
}
func fixture(t *testing.T, org string) (*access, *signer, *application.Downloads, *http.ServeMux) {
	t.Helper()
	a := &access{org: org, allowed: true, grants: map[feature.Key]bool{feature.KeyVerifyBasic: true, feature.KeyEnterpriseSelfHosted: true, feature.KeyEnterpriseAirgap: true, feature.KeyEnterpriseLTS: true}}
	config := aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "test-only-secret", SessionToken: "session-fixture"}, nil
	})}
	real, err := downloads.NewS3Signer(config, "https://objects.example.test", "private-releases")
	if err != nil {
		t.Fatal(err)
	}
	s := &signer{actual: real, a: a}
	service, err := application.NewDownloads(application.PlatformAccess{Tenants: tenants{a}, Entitlements: grants{a}, Permissions: a, Quotas: a}, artifacts(), s, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := downloads.NewHandler(service, a)
	mux := http.NewServeMux()
	h.Register(mux)
	return a, s, service, mux
}
func request(mux *http.ServeMux, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestEveryEditionProtectedDownloadBoundaries(t *testing.T) {
	for _, ed := range edition.Editions() {
		for _, tc := range []struct {
			id  string
			key feature.Key
		}{{"host", feature.KeyEnterpriseSelfHosted}, {"airgap", feature.KeyEnterpriseAirgap}, {"airgap", feature.KeyEnterpriseSelfHosted}, {"lts", feature.KeyEnterpriseLTS}, {"host_lts", feature.KeyEnterpriseLTS}, {"host_lts", feature.KeyEnterpriseSelfHosted}} {
			t.Run(ed.Name+"/"+tc.id+"/"+string(tc.key), func(t *testing.T) {
				a, s, service, mux := fixture(t, "org_"+string(ed.Key))
				path := "/v1/organizations/" + a.org + "/downloads/" + tc.id
				w := request(mux, "POST", path, "valid")
				if w.Code != 200 || s.calls != 1 {
					t.Fatal(w.Code, w.Body.String())
				}
				if a.stages[0] != "authenticate" || a.stages[1] != "tenant" || a.stages[len(a.stages)-2] != "permission" || a.stages[len(a.stages)-1] != "sign" {
					t.Fatal(a.stages)
				}
				var link application.DownloadLink
				if json.Unmarshal(w.Body.Bytes(), &link) != nil {
					t.Fatal("bad response")
				}
				verifySignature(t, link.URL)
				if time.Until(link.ExpiresAt) <= 0 || time.Until(link.ExpiresAt) > time.Minute || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("unbounded/cacheable link")
				}
				before, _ := json.Marshal(service.PublicCatalog())
				a.grants[tc.key] = false
				if w = request(mux, "POST", path, "valid"); w.Code != 403 || s.calls != 1 {
					t.Fatal("non-entitled signed", w.Code)
				}
				a.grants[tc.key] = true
				a.allowed = false
				if w = request(mux, "POST", path, "valid"); w.Code != 403 || s.calls != 1 {
					t.Fatal("authorization bypass")
				}
				a.allowed = true
				if w = request(mux, "POST", "/v1/organizations/other/downloads/"+tc.id, "valid"); w.Code != 403 || s.calls != 1 {
					t.Fatal("cross-tenant access")
				}
				a.fail = true
				if w = request(mux, "POST", path, "valid"); w.Code != 403 || strings.Contains(w.Body.String(), "private") || s.calls != 1 {
					t.Fatal("source failure opened access")
				}
				a.fail = false
				after, _ := json.Marshal(service.PublicCatalog())
				if string(before) != string(after) {
					t.Fatal("plan change mutated catalog")
				}
				if w = request(mux, "POST", path, "valid"); w.Code != 200 || s.calls != 2 {
					t.Fatal("restored grant inaccessible")
				}
			})
		}
	}
}

// Independently recompute AWS SigV4, including GET, the exact private key,
// short TTL, object version and response headers in the canonical query.
func verifySignature(t *testing.T, raw string) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	signature := q.Get("X-Amz-Signature")
	q.Del("X-Amz-Signature")
	seconds, e := strconv.Atoi(q.Get("X-Amz-Expires"))
	if e != nil || seconds < 1 || seconds > 60 || q.Get("X-Amz-SignedHeaders") != "host" || q.Get("response-cache-control") != "private, no-store" {
		t.Fatal(q)
	}
	var parts []string
	for key, values := range q {
		for _, v := range values {
			parts = append(parts, strings.ReplaceAll(url.QueryEscape(key), "+", "%20")+"="+strings.ReplaceAll(url.QueryEscape(v), "+", "%20"))
		}
	}
	sort.Strings(parts)
	canonical := "GET\n" + u.EscapedPath() + "\n" + strings.Join(parts, "&") + "\nhost:" + u.Host + "\n\nhost\nUNSIGNED-PAYLOAD"
	hash := sha256.Sum256([]byte(canonical))
	credential := strings.Split(q.Get("X-Amz-Credential"), "/")
	if len(credential) != 5 {
		t.Fatal(credential)
	}
	toSign := "AWS4-HMAC-SHA256\n" + q.Get("X-Amz-Date") + "\n" + strings.Join(credential[1:], "/") + "\n" + hex.EncodeToString(hash[:])
	mac := func(key []byte, value string) []byte {
		m := hmac.New(sha256.New, key)
		_, _ = m.Write([]byte(value))
		return m.Sum(nil)
	}
	key := mac([]byte("AWS4test-only-secret"), credential[1])
	key = mac(key, credential[2])
	key = mac(key, "s3")
	key = mac(key, "aws4_request")
	if hex.EncodeToString(mac(key, toSign)) != signature {
		t.Fatal("invalid signature")
	}
}

func TestPublicCatalogAndHTTPAdversarialInputs(t *testing.T) {
	_, s, _, mux := fixture(t, "org")
	w := request(mux, "GET", "/v1/downloads/public", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "airgap") || strings.Contains(w.Body.String(), "private-releases") || strings.Contains(w.Body.String(), "object_version") {
		t.Fatal(w.Body.String())
	}
	for _, id := range []string{"cli", "checksums"} {
		if w := request(mux, "GET", "/v1/downloads/public/"+id, ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, id := range []string{"host", "airgap", "lts", "host_lts", "missing"} {
		if w := request(mux, "GET", "/v1/downloads/public/"+id, ""); w.Code != 404 {
			t.Fatal("public bypass", id)
		}
	}
	before := s.calls
	for _, path := range []string{"/v1/organizations/org/downloads/host?edition=enterprise", "/v1/organizations/org/downloads/%2e%2e%2fhost", "/v1/organizations/org/downloads/host?key=public"} {
		if w := request(mux, "POST", path, "valid"); w.Code == 200 {
			t.Fatal(path)
		}
	}
	for _, token := range []string{"", "invalid", "valid,other"} {
		if w := request(mux, "POST", "/v1/organizations/org/downloads/host", token); w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/v1/organizations/org/downloads/host", nil)
	r.Header.Add("Authorization", "Bearer valid")
	r.Header.Add("Authorization", "Bearer other")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("duplicate auth")
	}
	r = httptest.NewRequest("POST", "/v1/organizations/org/downloads/host", strings.NewReader(`{"url":"https://attacker"}`))
	r.Header.Set("Authorization", "Bearer valid")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("body override")
	}
	if s.calls != before {
		t.Fatal("hostile request reached signer")
	}
}
