package application

import (
	"context"
	"io"
	"regexp"

	"safepatch/internal/authorization"
)

// ReleaseArtifactSource is a trusted private distribution adapter. Implementations
// resolve only immutable artifacts belonging to this exact version/channel and
// respect cancellation. They must not expose public bypass URLs or trust a client
// supplied storage key. The source is never called before shared admission.
type ReleaseArtifactSource interface {
	Open(context.Context, Release, string) (io.ReadCloser, error)
}

type ReleaseArtifacts struct {
	releases *ReleaseService
	source   ReleaseArtifactSource
}

var releaseArtifactName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

func NewReleaseArtifacts(releases *ReleaseService, source ReleaseArtifactSource) (*ReleaseArtifacts, error) {
	if releases == nil || source == nil {
		return nil, ErrInvalidGuard
	}
	return &ReleaseArtifacts{releases, source}, nil
}

func (s *ReleaseArtifacts) Open(ctx context.Context, actor authorization.Principal, org, version, channel, name string) (io.ReadCloser, error) {
	if s == nil || s.releases == nil || s.source == nil || ctx == nil || ctx.Err() != nil || !releaseArtifactName.MatchString(name) {
		return nil, ErrReleaseUnavailable
	}
	r, err := s.releases.Eligible(ctx, actor, org, version, channel)
	if err != nil {
		return nil, err
	}
	body, err := s.source.Open(ctx, r, name)
	if err != nil || body == nil || ctx.Err() != nil {
		if body != nil {
			_ = body.Close()
		}
		return nil, ErrReleaseUnavailable
	}
	return body, nil
}

// SupportEligible reuses the identical channel admission and support deadline.
// A channel selection alone does not establish a paid support entitlement.
func (s *ReleaseService) SupportEligible(ctx context.Context, actor authorization.Principal, org, version, channel string) (Release, error) {
	return s.Eligible(ctx, actor, org, version, channel)
}
