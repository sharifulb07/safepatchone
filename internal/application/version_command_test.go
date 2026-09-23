package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"safepatch/internal/domain/edition"
	grants "safepatch/internal/domain/entitlement"
	"safepatch/internal/domain/release"
)

type versionProvider struct {
	metadata VersionMetadata
	err      error
	calls    int
}

func (p *versionProvider) VersionMetadata(context.Context) (VersionMetadata, error) {
	p.calls++
	return p.metadata, p.err
}

func teamVersionProvider() *versionProvider {
	return &versionProvider{metadata: VersionMetadata{edition.Team, "team-2026-v1", release.Stable, grants.SourceSubscription}}
}

func setTestBuildVersion(t *testing.T, value string) {
	t.Helper()
	previous := BuildVersion
	BuildVersion = value
	t.Cleanup(func() { BuildVersion = previous })
}

func TestVersionRequestedFormatAndAliases(t *testing.T) {
	setTestBuildVersion(t, "1.0.0")
	want := "SafePatch:\n1.0.0\n\nProtocol:\n1\n\nEdition:\nTeam\n\nEdition configuration:\nteam-2026-v1\n\nRelease channel:\nstable\n\nEntitlement source:\nsubscription\n"
	for _, alias := range []string{"version", "--version"} {
		var out, errs bytes.Buffer
		if code := RunWithVersionMetadata("safepatch", []string{alias}, &out, &errs, teamVersionProvider()); code != 0 || out.String() != want || errs.Len() != 0 {
			t.Fatal(code, out.String(), errs.String())
		}
	}
}

func TestVersionEditionChannelAndSourceAreIndependent(t *testing.T) {
	setTestBuildVersion(t, "1.0.0")
	p := teamVersionProvider()
	for _, ed := range edition.Editions() {
		for _, channel := range release.Channels() {
			for _, source := range []grants.Source{grants.SourceCommunityDefault, grants.SourceSubscription, grants.SourceTrial, grants.SourceEnterpriseLicense, grants.SourcePromotion, grants.SourceManualOverride} {
				p.metadata = VersionMetadata{ed.Key, string(ed.Key) + "-2026-v1", channel, source}
				before := p.metadata
				var out, errs bytes.Buffer
				if code := RunWithVersionMetadata("safepatch", []string{"version", "--json"}, &out, &errs, p); code != 0 || errs.Len() != 0 {
					t.Fatal(code, errs.String())
				}
				var got versionInfo
				if err := json.Unmarshal(out.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Version != "1.0.0" || got.Protocol != 1 || got.Commit != BuildCommit || got.BuiltAt != BuildTime || got.Edition != ed.Key || got.EditionConfiguration != before.EditionConfiguration || got.ReleaseChannel != channel || got.EntitlementSource != strings.ToLower(string(source)) || p.metadata != before {
					t.Fatal("inspection changed or conflated independent metadata", got, before)
				}
			}
		}
	}
	if p.calls != 120 {
		t.Fatal("metadata was cached across state changes", p.calls)
	}
}

func TestVersionRejectsUntrustedOrUnavailableMetadata(t *testing.T) {
	for name, change := range map[string]func(*versionProvider){
		"error":              func(p *versionProvider) { p.err = errors.New("SECRET signed license data") },
		"unknown edition":    func(p *versionProvider) { p.metadata.Edition = "SECRET" },
		"wrong bundle":       func(p *versionProvider) { p.metadata.EditionConfiguration = "enterprise-2026-v1" },
		"terminal injection": func(p *versionProvider) { p.metadata.EditionConfiguration = "team-2026-v1\nSECRET\x1b[2J" },
		"oversized bundle":   func(p *versionProvider) { p.metadata.EditionConfiguration = strings.Repeat("x", 10000) },
		"missing source":     func(p *versionProvider) { p.metadata.EntitlementSource = "" },
		"unknown source":     func(p *versionProvider) { p.metadata.EntitlementSource = "SECRET" },
		"unknown channel":    func(p *versionProvider) { p.metadata.ReleaseChannel = "SECRET" },
		"empty channel":      func(p *versionProvider) { p.metadata.ReleaseChannel = "" },
	} {
		t.Run(name, func(t *testing.T) {
			for _, jsonMode := range []bool{false, true} {
				p := teamVersionProvider()
				change(p)
				var out, errs bytes.Buffer
				if code := versionCommand(context.Background(), p, cliOutput{&out, &errs, jsonMode}); code != 1 || out.Len() != 0 || strings.Contains(errs.String(), "SECRET") || !strings.Contains(errs.String(), "ENTITLEMENT_UNAVAILABLE") {
					t.Fatal(code, out.String(), errs.String())
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		p := teamVersionProvider()
		if versionCommand(ctx, p, cliOutput{io.Discard, io.Discard, false}) != 1 || p.calls != 0 {
			t.Fatal("used canceled or missing context")
		}
	}
	if versionCommand(context.Background(), nil, cliOutput{io.Discard, io.Discard, false}) != 1 {
		t.Fatal("nil provider fell back to Community")
	}
}

func TestVersionRejectsEditionInSoftwareVersion(t *testing.T) {
	for _, value := range []string{"1.0.0-enterprise", "1.0.0-team", "Team", "1.0.0\nPASS", ""} {
		t.Run(value, func(t *testing.T) {
			setTestBuildVersion(t, value)
			var out, errs bytes.Buffer
			if Run("safepatch", []string{"version"}, &out, &errs) != 1 || out.Len() != 0 {
				t.Fatal(out.String())
			}
		})
	}
}

type versionShortWriter struct{}

func (versionShortWriter) Write([]byte) (int, error) { return 0, nil }

func TestVersionUsageAndOutputFailures(t *testing.T) {
	for _, args := range [][]string{{"version", "--edition=enterprise"}, {"version", "extra"}, {"version", "--json", "--json"}} {
		p := teamVersionProvider()
		var out, errs bytes.Buffer
		if RunWithVersionMetadata("safepatch", args, &out, &errs, p) != 2 || out.Len() != 0 || p.calls != 0 {
			t.Fatal(args, out.String())
		}
	}
	for _, jsonMode := range []bool{false, true} {
		for _, w := range []io.Writer{nil, versionShortWriter{}} {
			var errs bytes.Buffer
			if versionCommand(context.Background(), teamVersionProvider(), cliOutput{w, &errs, jsonMode}) != 1 || !strings.Contains(errs.String(), "OUTPUT_ERROR") {
				t.Fatal(errs.String())
			}
		}
	}
}

func TestVersionMetadataDoesNotGrantEntitlements(t *testing.T) {
	p := teamVersionProvider()
	p.metadata = VersionMetadata{edition.Enterprise, "enterprise-2026-v1", "LTS", grants.SourceEnterpriseLicense}
	var version, out, errs bytes.Buffer
	if RunWithVersionMetadata("safepatch", []string{"version", "--json"}, &version, &errs, p) != 0 || !strings.Contains(version.String(), `"release_channel": "lts"`) {
		t.Fatal(version.String(), errs.String())
	}
	if RunWithVersionMetadata("safepatch", []string{"entitlement", "show", "--json"}, &out, &errs, p) != 0 || !strings.Contains(out.String(), `"edition": "community"`) || p.calls != 1 {
		t.Fatal("inspection granted access", out.String())
	}
}
