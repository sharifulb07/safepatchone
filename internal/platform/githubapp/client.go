package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"safepatch/internal/application"
	"safepatch/internal/domain/jsonobject"
	flow "safepatch/internal/domain/onboarding"
	"safepatch/internal/trust"
	"strconv"
	"strings"
	"time"
)

type ClientConfig struct {
	AppID         int64
	PrivateKeyPEM []byte
	Transport     http.RoundTripper
	Now           func() time.Time
}
type Client struct {
	appID int64
	key   *rsa.PrivateKey
	http  *http.Client
	now   func() time.Time
}

func NewClient(c ClientConfig) (*Client, error) {
	if c.AppID <= 0 || c.Now == nil || len(c.PrivateKeyPEM) > 16384 {
		return nil, application.ErrChangeWorkflow
	}
	block, rest := pem.Decode(c.PrivateKeyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, application.ErrChangeWorkflow
	}
	var key *rsa.PrivateKey
	if k, e := x509.ParsePKCS1PrivateKey(block.Bytes); e == nil {
		key = k
	} else if k, e := x509.ParsePKCS8PrivateKey(block.Bytes); e == nil {
		key, _ = k.(*rsa.PrivateKey)
	}
	if key == nil || key.N.BitLen() < 2048 || key.Validate() != nil {
		return nil, application.ErrChangeWorkflow
	}
	transport := c.Transport
	if transport == nil {
		transport = &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxConnsPerHost: 16, IdleConnTimeout: time.Minute, ForceAttemptHTTP2: true}
	}
	return &Client{c.AppID, key, &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, c.Now}, nil
}
func (c *Client) jwt() (string, error) {
	now := c.now()
	payload, _ := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(8 * time.Minute).Unix(), "iss": strconv.FormatInt(c.appID, 10)})
	value := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(value))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", application.ErrChangeWorkflow
	}
	return value + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (c *Client) call(ctx context.Context, method, path, token string, input, output any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n\\") || strings.ContainsAny(token, "\r\n") || len(token) > 8192 {
		return application.ErrChangeWorkflow
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return application.ErrChangeWorkflow
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, body)
	if err != nil {
		return application.ErrChangeWorkflow
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "SafePatch-App")
	res, err := c.http.Do(req)
	if err != nil {
		return application.ErrChangeWorkflow
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return application.ErrChangeWorkflow
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return application.ErrChangeWorkflow
	}
	raw, err = jsonobject.Normalize(raw, 2*1024*1024)
	if err != nil || json.Unmarshal(raw, output) != nil {
		return application.ErrChangeWorkflow
	}
	return nil
}
func (c *Client) token(ctx context.Context, p application.PullRequest) (string, error) {
	if p.Validate() != nil {
		return "", application.ErrChangeWorkflow
	}
	jwt, err := c.jwt()
	if err != nil {
		return "", err
	}
	var reply struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	err = c.call(ctx, "POST", fmt.Sprintf("/app/installations/%d/access_tokens", p.InstallationID), jwt, map[string]any{"repository_ids": []int64{p.RepositoryID}, "permissions": map[string]string{"contents": "read", "pull_requests": "read", "checks": "write", "metadata": "read"}}, &reply)
	if err != nil || len(reply.Token) < 16 || len(reply.Token) > 4096 || !reply.ExpiresAt.After(c.now()) || reply.ExpiresAt.After(c.now().Add(2*time.Hour)) {
		return "", application.ErrChangeWorkflow
	}
	return reply.Token, nil
}
func (c *Client) repository(ctx context.Context, p application.PullRequest, token string) (string, bool, error) {
	var repo struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Private  *bool  `json:"private"`
	}
	err := c.call(ctx, "GET", fmt.Sprintf("/repositories/%d", p.RepositoryID), token, nil, &repo)
	if err != nil || repo.ID != p.RepositoryID || repo.Private == nil || (flow.Repository{ID: "repo", FullName: repo.FullName}).Validate() != nil {
		return "", false, application.ErrChangeWorkflow
	}
	return repo.FullName, *repo.Private, nil
}

// Current re-reads authoritative repository visibility and both PR pins. A
// signed old delivery cannot establish that a PR is still open/current.
func (c *Client) Current(ctx context.Context, p application.PullRequest) (application.PullRequest, bool, error) {
	token, err := c.token(ctx, p)
	if err != nil {
		return p, false, err
	}
	name, private, err := c.repository(ctx, p, token)
	if err != nil {
		return p, false, err
	}
	var pr struct {
		Number int64  `json:"number"`
		State  string `json:"state"`
		Base   struct {
			SHA  string `json:"sha"`
			Repo struct {
				ID int64 `json:"id"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err = c.call(ctx, "GET", fmt.Sprintf("/repos/%s/pulls/%d", name, p.Number), token, nil, &pr); err != nil {
		return p, false, err
	}
	if pr.Number != p.Number || pr.Base.Repo.ID != p.RepositoryID {
		return p, false, application.ErrChangeWorkflow
	}
	current := p
	current.BaseSHA = pr.Base.SHA
	current.HeadSHA = pr.Head.SHA
	current.Private = private
	if current.Validate() != nil {
		return p, false, application.ErrChangeWorkflow
	}
	return current, pr.State == "open" && current.BaseSHA == p.BaseSHA && current.HeadSHA == p.HeadSHA, nil
}

// Publish never emits a success for stale pins, missing evidence or UNKNOWN.
// On ambiguous create failure, retries find the exact app/external_id/head before
// creating again. GitHub does not promise transactional exactly-once Checks.
func (c *Client) Publish(ctx context.Context, j application.PullRequestJob, result application.ChangeResult) error {
	token, err := c.token(ctx, j.Request)
	if err != nil {
		return err
	}
	name, _, err := c.repository(ctx, j.Request, token)
	if err != nil {
		return err
	}
	conclusion := "action_required"
	if result.EvidenceReference != "" && result.ChangeID == j.Change().ID() {
		if result.Decision == trust.Pass {
			conclusion = "success"
		} else if result.Decision == trust.Fail {
			conclusion = "failure"
		}
	}
	current, valid, err := c.Current(ctx, j.Request)
	if err != nil {
		return err
	}
	if !valid || current.Private != j.Request.Private {
		conclusion = "cancelled"
	}
	var checkID int64
	for page := 1; page <= 10; page++ {
		var list struct {
			Total  int `json:"total_count"`
			Checks []struct {
				ID         int64  `json:"id"`
				ExternalID string `json:"external_id"`
				Head       string `json:"head_sha"`
				App        struct {
					ID int64 `json:"id"`
				} `json:"app"`
			} `json:"check_runs"`
		}
		err = c.call(ctx, "GET", fmt.Sprintf("/repos/%s/commits/%s/check-runs?filter=all&per_page=100&page=%d", name, j.Request.HeadSHA, page), token, nil, &list)
		if err != nil {
			return err
		}
		if list.Total < 0 || list.Total > 1000 {
			return application.ErrChangeWorkflow
		}
		for _, v := range list.Checks {
			if v.ExternalID == j.ID && v.App.ID == c.appID && v.Head == j.Request.HeadSHA {
				if checkID != 0 || v.ID <= 0 {
					return application.ErrChangeWorkflow
				}
				checkID = v.ID
			}
		}
		if (page-1)*100+len(list.Checks) == list.Total {
			break
		}
		if page == 10 || len(list.Checks) != 100 {
			return application.ErrChangeWorkflow
		}
	}
	body := map[string]any{"name": "SafePatch", "external_id": j.ID, "status": "completed", "conclusion": conclusion, "completed_at": c.now().UTC().Format(time.RFC3339), "output": map[string]string{"title": "SafePatch verification", "summary": "Decision: " + conclusion + ". Change: " + j.Change().ID() + ". This check does not authorize production deployment."}}
	endpoint := fmt.Sprintf("/repos/%s/check-runs", name)
	method := "POST"
	if checkID > 0 {
		method = "PATCH"
		endpoint += fmt.Sprintf("/%d", checkID)
	} else {
		body["head_sha"] = j.Request.HeadSHA
	}
	var reply struct {
		ID         int64  `json:"id"`
		Head       string `json:"head_sha"`
		ExternalID string `json:"external_id"`
	}
	if err = c.call(ctx, method, endpoint, token, body, &reply); err != nil {
		return err
	}
	if reply.ID <= 0 || reply.Head != j.Request.HeadSHA || reply.ExternalID != j.ID {
		return application.ErrChangeWorkflow
	}
	return nil
}
