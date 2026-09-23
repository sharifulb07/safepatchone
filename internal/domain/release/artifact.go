package release

import (
	"errors"
	"regexp"
	"time"

	"safepatch/internal/domain/feature"
	"safepatch/internal/domain/organization"
)

type ArtifactKind string

const (
	CLI              ArtifactKind = "cli"
	PublicAsset      ArtifactKind = "public_asset"
	SelfHostedBundle ArtifactKind = "self_host_bundle"
	AirgapBundle     ArtifactKind = "airgap_bundle"
)

var ErrArtifact = errors.New("invalid release artifact")
var artifactFile = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var artifactHash = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Artifact is trusted publication metadata, never a request-supplied storage key.
// OrganizationID optionally restricts a private artifact to one organization.
type Artifact struct {
	ID             string       `json:"id"`
	Version        string       `json:"version"`
	Channel        string       `json:"channel"`
	Kind           ArtifactKind `json:"kind"`
	Filename       string       `json:"filename"`
	SHA256         string       `json:"sha256"`
	Bytes          int64        `json:"bytes"`
	AvailableUntil time.Time    `json:"available_until"`
	OrganizationID string       `json:"organization_id,omitempty"`
	ObjectVersion  string       `json:"object_version,omitempty"`
}

func (a Artifact) Normalize() (Artifact, error) {
	c, err := ParseChannel(a.Channel)
	if err != nil || !organization.ValidID(a.ID) || !ValidVersion(a.Version) || !artifactFile.MatchString(a.Filename) || !artifactHash.MatchString(a.SHA256) || a.Bytes <= 0 || a.Bytes > 1<<40 || a.AvailableUntil.IsZero() || a.AvailableUntil.Year() < 1 || a.AvailableUntil.Year() > 9999 || (a.OrganizationID != "" && !organization.ValidID(a.OrganizationID)) || len(a.ObjectVersion) > 1024 {
		return Artifact{}, ErrArtifact
	}
	for _, r := range a.ObjectVersion {
		if r < 33 || r > 126 {
			return Artifact{}, ErrArtifact
		}
	}
	switch a.Kind {
	case CLI, PublicAsset, SelfHostedBundle, AirgapBundle:
	default:
		return Artifact{}, ErrArtifact
	}
	a.Channel = string(c)
	return a, nil
}

func (a Artifact) Public() bool {
	return a.Channel == string(Stable) && a.OrganizationID == "" && (a.Kind == CLI || a.Kind == PublicAsset)
}

// RequiredFeatures is independent of edition; LTS adds to bundle requirements.
func (a Artifact) RequiredFeatures() []feature.Key {
	var keys []feature.Key
	switch a.Kind {
	case SelfHostedBundle:
		keys = append(keys, feature.KeyEnterpriseSelfHosted)
	case AirgapBundle:
		keys = append(keys, feature.KeyEnterpriseSelfHosted, feature.KeyEnterpriseAirgap)
	default:
		keys = append(keys, feature.KeyVerifyBasic)
	}
	if a.Channel == string(LTS) {
		keys = append(keys, feature.KeyEnterpriseLTS)
	}
	return keys
}

// Key is constructed only from validated catalog fields. Signed URLs never
// accept request-controlled bucket, key, filename or response header overrides.
func (a Artifact) Key() string { return "releases/" + a.SHA256 + "/" + a.Filename }
