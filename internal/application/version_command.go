package application

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"safepatch/internal/domain/edition"
	grants "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/release"
	"safepatch/internal/entitlement"
)

// ProtocolVersion evolves independently of software and commercial configuration.
const ProtocolVersion = 1

// VersionMetadata is inspection data, never an entitlement snapshot or credential.
// The provider must resolve the selected organization's authoritative state and
// trusted deployment channel, not infer either from user flags or repository data.
type VersionMetadata struct {
	Edition              edition.Key
	EditionConfiguration string
	ReleaseChannel       release.Channel
	EntitlementSource    grants.Source
}

type VersionMetadataProvider interface {
	VersionMetadata(context.Context) (VersionMetadata, error)
}

type localVersionMetadata struct{}

func (localVersionMetadata) VersionMetadata(ctx context.Context) (VersionMetadata, error) {
	p, err := entitlement.NewCommunityEntitlementProvider()
	if err != nil {
		return VersionMetadata{}, err
	}
	report, err := p.Inspect(ctx)
	if err != nil {
		return VersionMetadata{}, err
	}
	channel, err := release.ParseChannel(BuildChannel)
	return VersionMetadata{report.Edition, report.BundleVersion, channel, report.Source}, err
}

type versionInfo struct {
	buildInfo
	Protocol             int             `json:"protocol"`
	Edition              edition.Key     `json:"edition"`
	EditionConfiguration string          `json:"edition_configuration"`
	ReleaseChannel       release.Channel `json:"release_channel"`
	EntitlementSource    string          `json:"entitlement_source"`
}

var versionConfigurationPattern = regexp.MustCompile(`^([a-z]+)-[1-9][0-9]{3}-v[1-9][0-9]{0,8}$`)

func versionCommand(ctx context.Context, provider VersionMetadataProvider, out cliOutput) int {
	fail := func() int {
		return out.failure(&CLIError{Code: CodeEntitlementUnavailable, Message: "version metadata unavailable"}, 1)
	}
	if provider == nil || ctx == nil || ctx.Err() != nil {
		return fail()
	}
	m, err := provider.VersionMetadata(ctx)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	ed, err := edition.Lookup(m.Edition)
	if err != nil {
		return fail()
	}
	parts := versionConfigurationPattern.FindStringSubmatch(m.EditionConfiguration)
	if len(m.EditionConfiguration) > 64 || len(parts) != 2 || parts[1] != string(ed.Key) {
		return fail()
	}
	channel, err := release.ParseChannel(string(m.ReleaseChannel))
	if err != nil {
		return fail()
	}
	switch m.EntitlementSource {
	case grants.SourceCommunityDefault, grants.SourceSubscription, grants.SourceTrial,
		grants.SourceEnterpriseLicense, grants.SourcePromotion, grants.SourceManualOverride:
	default:
		return fail()
	}
	// Match the shared release pipeline's numeric version contract. A local
	// checkout reports dev rather than claiming to be a published release.
	if BuildVersion != "dev" && !release.ValidVersion(BuildVersion) {
		return fail()
	}
	info := versionInfo{buildInfo{BuildVersion, BuildCommit, BuildTime}, ProtocolVersion,
		ed.Key, m.EditionConfiguration, channel, strings.ToLower(string(m.EntitlementSource))}
	return out.success(info, fmt.Sprintf("SafePatch:\n%s\n\nProtocol:\n%d\n\nEdition:\n%s\n\nEdition configuration:\n%s\n\nRelease channel:\n%s\n\nEntitlement source:\n%s\n",
		info.Version, info.Protocol, ed.Name, info.EditionConfiguration, info.ReleaseChannel, info.EntitlementSource))
}
