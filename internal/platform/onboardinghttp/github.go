// Package onboardinghttp exposes authenticated organization onboarding. Provider
// credentials, OAuth and HTTP remain outside the shared domain architecture.
package onboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"safepatch/internal/authorization"
	flow "safepatch/internal/domain/onboarding"
	"strconv"
	"strings"
	"time"
)

type GitHubConfig struct {
	ClientID, ClientSecret, AppSlug, CallbackURL string
	Transport                                    http.RoundTripper
}
type GitHub struct {
	config   GitHubConfig
	client   *http.Client
	sessions *Sessions
}
type Installation struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func NewGitHub(c GitHubConfig, s *Sessions) (*GitHub, error) {
	u, err := url.Parse(c.CallbackURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(c.ClientID) < 5 || len(c.ClientSecret) < 16 || s == nil || !regexp.MustCompile(`^[a-z0-9-]{1,100}$`).MatchString(c.AppSlug) {
		return nil, flow.ErrInvalid
	}
	transport := c.Transport
	if transport == nil {
		transport = &http.Transport{TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, MaxConnsPerHost: 20, IdleConnTimeout: time.Minute, ForceAttemptHTTP2: true}
	}
	return &GitHub{c, &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, s}, nil
}
func (g *GitHub) AuthorizeURL(state, challenge string) string {
	q := url.Values{"client_id": {g.config.ClientID}, "redirect_uri": {g.config.CallbackURL}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return "https://github.com/login/oauth/authorize?" + q.Encode()
}
func (g *GitHub) InstallURL() string {
	return "https://github.com/apps/" + g.config.AppSlug + "/installations/new"
}
func (g *GitHub) call(ctx context.Context, method, target, token string, body io.Reader, out any) error {
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" || (u.Host != "api.github.com" && target != "https://github.com/login/oauth/access_token") || u.User != nil {
		return flow.ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return flow.ErrInvalid
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SafePatch-onboarding")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if token != "" {
		if len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
			return flow.ErrInvalid
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	res, err := g.client.Do(req)
	if err != nil {
		return flow.ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return flow.ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 || json.Unmarshal(raw, out) != nil {
		return flow.ErrUnavailable
	}
	return nil
}
func (g *GitHub) Exchange(ctx context.Context, code, verifier string) (string, string, time.Time, error) {
	if len(code) < 1 || len(code) > 1024 {
		return "", "", time.Time{}, flow.ErrInvalid
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	q := url.Values{"client_id": {g.config.ClientID}, "client_secret": {g.config.ClientSecret}, "redirect_uri": {g.config.CallbackURL}, "code": {code}, "code_verifier": {verifier}}
	if err := g.call(ctx, "POST", "https://github.com/login/oauth/access_token", "", bytes.NewBufferString(q.Encode()), &token); err != nil {
		return "", "", time.Time{}, err
	}
	if token.TokenType != "bearer" || len(token.AccessToken) < 16 || len(token.AccessToken) > 4096 {
		return "", "", time.Time{}, flow.ErrUnavailable
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := g.call(ctx, "GET", "https://api.github.com/user", token.AccessToken, nil, &user); err != nil {
		return "", "", time.Time{}, err
	}
	if user.ID <= 0 || len(user.Login) < 1 || len(user.Login) > 100 {
		return "", "", time.Time{}, flow.ErrUnavailable
	}
	duration := time.Hour
	if token.ExpiresIn > 0 && token.ExpiresIn <= 28800 {
		duration = time.Duration(token.ExpiresIn) * time.Second
	}
	return g.sessions.login(ctx, strconv.FormatInt(user.ID, 10), user.Login, token.AccessToken, g.sessions.now().Add(duration))
}
func (g *GitHub) Installations(ctx context.Context, actor authorization.Principal) ([]Installation, error) {
	token, err := g.sessions.token(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	result := []Installation{}
	for page := 1; page <= 10; page++ {
		var data struct {
			Total         int `json:"total_count"`
			Installations []struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installations"`
		}
		if err = g.call(ctx, "GET", fmt.Sprintf("https://api.github.com/user/installations?per_page=100&page=%d", page), token, nil, &data); err != nil {
			return nil, err
		}
		if data.Total > 1000 || data.Total < 0 {
			return nil, flow.ErrUnavailable
		}
		for _, i := range data.Installations {
			if i.ID <= 0 || len(i.Account.Login) > 100 {
				return nil, flow.ErrUnavailable
			}
			result = append(result, Installation{i.ID, i.Account.Login})
		}
		if len(result) == data.Total {
			return result, nil
		}
		if len(data.Installations) < 100 {
			return nil, flow.ErrUnavailable
		}
	}
	return nil, flow.ErrUnavailable
}
func (g *GitHub) Repositories(ctx context.Context, actor authorization.Principal, installation int64) ([]flow.Repository, error) {
	if installation <= 0 {
		return nil, flow.ErrInvalid
	}
	token, err := g.sessions.token(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	result := []flow.Repository{}
	for page := 1; page <= 10; page++ {
		var data struct {
			Total        int `json:"total_count"`
			Repositories []struct {
				ID       int64  `json:"id"`
				FullName string `json:"full_name"`
				Private  *bool  `json:"private"`
				Archived bool   `json:"archived"`
				Disabled bool   `json:"disabled"`
			} `json:"repositories"`
		}
		if err = g.call(ctx, "GET", fmt.Sprintf("https://api.github.com/user/installations/%d/repositories?per_page=100&page=%d", installation, page), token, nil, &data); err != nil {
			return nil, err
		}
		if data.Total > 1000 || data.Total < 0 {
			return nil, flow.ErrUnavailable
		}
		for _, r := range data.Repositories {
			if r.Private == nil {
				return nil, flow.ErrUnavailable
			}
			v := flow.Repository{ID: fmt.Sprintf("gh_%d", r.ID), FullName: r.FullName, Private: *r.Private}
			if r.ID <= 0 || v.Validate() != nil {
				return nil, flow.ErrUnavailable
			}
			if !r.Archived && !r.Disabled {
				result = append(result, v)
			}
		}
		if (page-1)*100+len(data.Repositories) == data.Total {
			return result, nil
		}
		if len(data.Repositories) < 100 {
			return nil, flow.ErrUnavailable
		}
	}
	return nil, flow.ErrUnavailable
}
