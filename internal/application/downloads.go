package application

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"time"

	"safepatch/internal/authorization"
	"safepatch/internal/domain/release"
	"safepatch/internal/entitlement"
	"safepatch/internal/tenant"
)

var ErrDownloadDenied = errors.New("download not available")
var ErrDownloadUnavailable = errors.New("download signing unavailable")

const DownloadTTL = 60 * time.Second

type DownloadLink struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	Filename  string    `json:"filename"`
	SHA256    string    `json:"sha256"`
	Bytes     int64     `json:"bytes"`
}
type DownloadSigner interface {
	Sign(context.Context, release.Artifact, time.Time) (DownloadLink, error)
}
type PublicDownload struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Channel  string `json:"channel"`
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
}
type Downloads struct {
	access    PlatformAccess
	artifacts map[string]release.Artifact
	signer    DownloadSigner
	now       func() time.Time
}

func NewDownloads(access PlatformAccess, artifacts []release.Artifact, signer DownloadSigner, now func() time.Time) (*Downloads, error) {
	if access.Tenants == nil || access.Entitlements == nil || access.Permissions == nil || access.Quotas == nil || signer == nil || now == nil || len(artifacts) == 0 || len(artifacts) > 4096 {
		return nil, ErrInvalidGuard
	}
	s := &Downloads{access: access, artifacts: map[string]release.Artifact{}, signer: signer, now: now}
	for _, a := range artifacts {
		v, err := a.Normalize()
		if err != nil || s.artifacts[v.ID].ID != "" {
			return nil, ErrDownloadDenied
		}
		// Identical immutable bytes cannot be classified as both public and
		// protected in this publication catalog, even under different aliases.
		for _, prior := range s.artifacts {
			if prior.SHA256 == v.SHA256 && prior.Public() != v.Public() {
				return nil, ErrDownloadDenied
			}
		}
		s.artifacts[v.ID] = v
	}
	return s, nil
}

func (s *Downloads) PublicCatalog() []PublicDownload {
	out := []PublicDownload{}
	if s == nil {
		return out
	}
	for _, a := range s.artifacts {
		if a.Public() && s.now().Before(a.AvailableUntil) {
			out = append(out, PublicDownload{a.ID, a.Version, a.Channel, a.Filename, a.SHA256, a.Bytes})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Downloads) Public(ctx context.Context, id string) (DownloadLink, error) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return DownloadLink{}, ErrDownloadDenied
	}
	a, ok := s.artifacts[id]
	if !ok || !a.Public() {
		return DownloadLink{}, ErrDownloadDenied
	}
	return s.sign(ctx, a)
}
func (s *Downloads) Protected(ctx context.Context, actor authorization.Principal, org, id string) (DownloadLink, error) {
	a, err := s.authorize(ctx, actor, org, id)
	// Issuance preserves the existing opaque denial contract for all access
	// failures. The catalog distinguishes outages from an empty entitlement set.
	if err != nil {
		return DownloadLink{}, ErrDownloadDenied
	}
	return s.sign(ctx, a)
}

// OrganizationCatalog exposes only currently authorized publication metadata.
// Issuance rechecks access, so a catalog response never grants download rights.
func (s *Downloads) OrganizationCatalog(ctx context.Context, actor authorization.Principal, org string) ([]PublicDownload, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || actor.Validate() != nil || !tenant.ValidID(org) {
		return nil, ErrDownloadDenied
	}
	scope := tenant.Scope{OrganizationID: org, ResourceID: org}
	resolved, err := s.access.Tenants.Resolve(ctx, actor, scope)
	if err != nil || resolved != scope {
		return nil, ErrDownloadDenied
	}
	allowed, err := s.access.Permissions.Allowed(ctx, actor, org, authorization.KeyOrganizationManage)
	if err != nil {
		return nil, ErrDownloadUnavailable
	}
	if !allowed {
		return nil, ErrDownloadDenied
	}
	items := []PublicDownload{}
	for id, a := range s.artifacts {
		if a.Public() || !s.now().Before(a.AvailableUntil) {
			continue
		}
		if _, err := s.authorize(ctx, actor, org, id); err != nil {
			if errors.Is(err, ErrDownloadDenied) {
				continue
			}
			return nil, err
		}
		items = append(items, PublicDownload{a.ID, a.Version, a.Channel, a.Filename, a.SHA256, a.Bytes})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (s *Downloads) authorize(ctx context.Context, actor authorization.Principal, org, id string) (release.Artifact, error) {
	if s == nil || ctx == nil || ctx.Err() != nil || actor.Validate() != nil || !tenant.ValidID(org) {
		return release.Artifact{}, ErrDownloadDenied
	}
	scope := tenant.Scope{OrganizationID: org, ResourceID: org}
	resolved, err := s.access.Tenants.Resolve(ctx, actor, scope)
	if err != nil || resolved != scope {
		return release.Artifact{}, ErrDownloadDenied
	}
	a, ok := s.artifacts[id]
	if !ok || a.Public() {
		return release.Artifact{}, ErrDownloadDenied
	}
	keys := a.RequiredFeatures()
	for _, key := range keys {
		if err := entitlement.RequireEntitlement(ctx, s.access.Entitlements, org, key); err != nil {
			if !errors.Is(err, entitlement.ErrNotEntitled) {
				return release.Artifact{}, ErrDownloadUnavailable
			}
			return release.Artifact{}, ErrDownloadDenied
		}
	}
	allowed, err := s.access.Permissions.Allowed(ctx, actor, org, authorization.KeyOrganizationManage)
	if err != nil || !allowed || (a.OrganizationID != "" && a.OrganizationID != org) {
		return release.Artifact{}, ErrDownloadDenied
	}
	if err := s.access.Quotas.Check(ctx, Execution{Actor: actor, Scope: scope, Requirement: Requirement{Feature: keys[0], Permission: authorization.KeyOrganizationManage}}); err != nil {
		return release.Artifact{}, ErrDownloadDenied
	}
	return a, nil
}
func (s *Downloads) sign(ctx context.Context, a release.Artifact) (DownloadLink, error) {
	now := s.now()
	ttl := a.AvailableUntil.Sub(now)
	if ttl > DownloadTTL {
		ttl = DownloadTTL
	}
	ttl = ttl.Truncate(time.Second)
	if now.IsZero() || ttl < time.Second || ctx.Err() != nil {
		return DownloadLink{}, ErrDownloadDenied
	}
	v, err := s.signer.Sign(ctx, a, now.Add(ttl))
	if err != nil {
		return DownloadLink{}, ErrDownloadUnavailable
	}
	u, parseErr := url.Parse(v.URL)
	if err != nil || parseErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery == "" || !v.ExpiresAt.After(s.now()) || v.ExpiresAt.After(now.Add(ttl)) || ctx.Err() != nil {
		return DownloadLink{}, ErrDownloadUnavailable
	}
	v.Filename = a.Filename
	v.SHA256 = a.SHA256
	v.Bytes = a.Bytes
	return v, nil
}
