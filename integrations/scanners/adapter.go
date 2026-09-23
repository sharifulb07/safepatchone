// Package scanners adapts mature local scanning engines to the shared SDK.
// Executables, rules and databases are operator policy, never repository input.
package scanners

import (
	"context"
	"errors"
	"maps"
	"path"
	"regexp"
	"strings"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

type Tool string

const (
	Gitleaks Tool = "gitleaks"
	Semgrep  Tool = "semgrep"
	Trivy    Tool = "trivy"
	OSV      Tool = "osv"
	Syft     Tool = "syft"
)

// Adapter is a technical extension; it has no entitlement or edition input.
type Adapter interface {
	verification.Verifier
	Tool() Tool
	ToolVersion() string
	Environment() map[string]string
}

var ErrConfiguration = errors.New("invalid scanner policy")
var ErrOutput = errors.New("invalid scanner output")
var toolVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[A-Za-z0-9.-]+)?$`)

// Config is trusted composition. Paths name pre-provisioned resources outside
// the source tree (inside the pinned image for container execution). ConfigFile
// must explicitly select trusted rules/settings; IgnoreFile is a trusted empty
// file for Gitleaks/Trivy. DatabaseDirectory is required for Trivy/OSV.
// Version is the installed tool's pinned version, not the adapter version.
type Config struct {
	Executable        string
	Version           string
	ConfigFile        string
	IgnoreFile        string
	DatabaseDirectory string
}

type adapter struct {
	tool   Tool
	config Config
}

func New(tool Tool, config Config) (Adapter, error) {
	major := map[Tool]string{Gitleaks: "8.", Semgrep: "1.", Trivy: "0.", OSV: "2.", Syft: "1."}[tool]
	if major == "" || (config.Executable != "" && !verification.ValidToolName(config.Executable)) || !toolVersion.MatchString(config.Version) || !strings.HasPrefix(config.Version, major) {
		return nil, ErrConfiguration
	}
	for _, p := range []string{config.ConfigFile, config.IgnoreFile, config.DatabaseDirectory} {
		if p != "" && !trustedPath(p) {
			return nil, ErrConfiguration
		}
	}
	return &adapter{tool: tool, config: config}, nil
}

func trustedPath(p string) bool {
	p = strings.ReplaceAll(p, "\\", "/")
	if !evidence.ValidText(p, 4096) || strings.HasPrefix(p, "//") {
		return false
	}
	// Native drive paths and container absolute paths are accepted, never URLs.
	if len(p) > 3 && p[1] == ':' && p[2] == '/' {
		p = p[2:]
	}
	return strings.HasPrefix(p, "/") && p != "/" && !strings.Contains(p, ":") && path.Clean(p) == p && p != "/workspace" && !strings.HasPrefix(p, "/workspace/") && p != "/input" && !strings.HasPrefix(p, "/input/")
}

func (a *adapter) ID() string          { return "scanner." + string(a.tool) }
func (a *adapter) Version() string     { return "1.0.0" }
func (a *adapter) Tool() Tool          { return a.tool }
func (a *adapter) ToolVersion() string { return a.config.Version }
func (a *adapter) Capabilities() []verification.Capability {
	return []verification.Capability{verification.Capability(a.ID())}
}

func (a *adapter) Environment() map[string]string {
	env := map[string]string{}
	switch a.tool {
	case Semgrep:
		env["SEMGREP_SEND_METRICS"] = "off"
		env["SEMGREP_ENABLE_VERSION_CHECK"] = "0"
	case OSV:
		env["OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY"] = a.config.DatabaseDirectory
	case Syft:
		for _, key := range []string{"SYFT_CHECK_FOR_APP_UPDATE", "SYFT_GOLANG_SEARCH_REMOTE_LICENSES", "SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES", "SYFT_GOLANG_SEARCH_LOCAL_VENDOR_LICENSES", "SYFT_GOLANG_USE_PACKAGES_LIB", "SYFT_JAVA_USE_NETWORK", "SYFT_JAVA_USE_MAVEN_LOCAL_REPOSITORY", "SYFT_JAVASCRIPT_SEARCH_REMOTE_LICENSES", "SYFT_PYTHON_SEARCH_REMOTE_LICENSES", "SYFT_PACKAGE_SEARCH_UNINDEXED_ARCHIVES", "SYFT_PACKAGE_SEARCH_INDEXED_ARCHIVES"} {
			env[key] = "false"
		}
	}
	return env
}

func (a *adapter) Detect(ctx context.Context, target verification.Target) (verification.Detection, error) {
	if ctx == nil || target.Validate() != nil {
		return verification.Detection{}, verification.ErrInvalidVerifier
	}
	if err := ctx.Err(); err != nil {
		return verification.Detection{}, err
	}
	if len(target.Files) == 0 {
		return verification.Detection{State: verification.NotApplicable}, nil
	}
	return verification.Detection{State: verification.Detected, Capabilities: a.Capabilities()}, nil
}

func (a *adapter) Plan(ctx context.Context, input verification.PlanRequest) (verification.Plan, error) {
	if ctx == nil || input.Target.Validate() != nil || input.Capability != a.Capabilities()[0] || input.BaseExecution.Validate() != nil || !maps.Equal(input.BaseExecution.Environment, a.Environment()) {
		return verification.Plan{}, verification.ErrInvalidPlan
	}
	if err := ctx.Err(); err != nil {
		return verification.Plan{}, err
	}
	c := a.config
	if c.Executable == "" || c.ConfigFile == "" || ((a.tool == Gitleaks || a.tool == Trivy) && c.IgnoreFile == "") || ((a.tool == Trivy || a.tool == OSV) && c.DatabaseDirectory == "") {
		return verification.Plan{}, verification.ErrVerifierUnavailable
	}
	// Never let a repository supply policy or a vulnerability database.
	source := strings.ToLower(strings.TrimRight(strings.ReplaceAll(input.BaseExecution.SourceDirectory, "\\", "/"), "/"))
	for _, p := range []string{c.ConfigFile, c.IgnoreFile, c.DatabaseDirectory} {
		p = strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
		if p != "" && (p == source || strings.HasPrefix(p, source+"/")) {
			return verification.Plan{}, ErrConfiguration
		}
	}
	if a.tool == Semgrep {
		// Stable Semgrep CLI has no supported flag disabling nested ignore files.
		for _, f := range input.Target.Files {
			if strings.EqualFold(path.Base(f), ".semgrepignore") {
				return verification.Plan{}, ErrConfiguration
			}
		}
	}
	r := input.BaseExecution
	r.Executable = c.Executable
	r.WorkingDirectory = "."
	switch a.tool {
	case Gitleaks:
		r.Arguments = []string{"dir", "--config", c.ConfigFile, "--gitleaks-ignore-path", c.IgnoreFile, "--ignore-gitleaks-allow", "--report-format", "json", "--report-path", "-", "--exit-code", "1", "--no-banner", "--no-color", "--log-level", "error", "--max-archive-depth", "0", "--max-decode-depth", "0", "."}
	case Semgrep:
		r.Arguments = []string{"scan", "--config", c.ConfigFile, "--json", "--error", "--strict", "--quiet", "--metrics=off", "--disable-version-check", "--disable-nosem", "--no-git-ignore", "--oss-only", "--no-secrets-validation", "."}
	case Trivy:
		r.Arguments = []string{"filesystem", "--config", c.ConfigFile, "--ignorefile", c.IgnoreFile, "--cache-dir", c.DatabaseDirectory, "--cache-backend", "memory", "--format", "json", "--scanners", "vuln", "--list-all-pkgs", "--exit-code", "1", "--offline-scan", "--skip-db-update", "--skip-java-db-update", "--skip-check-update", "--disable-telemetry", "--quiet", "."}
	case OSV:
		r.Arguments = []string{"scan", "source", "--config", c.ConfigFile, "--format", "json", "--verbosity=error", "--all-packages", "--offline", "--no-resolve", "--no-call-analysis=go,rust", "--recursive", "."}
	case Syft:
		r.Arguments = []string{"scan", "dir:.", "--config", c.ConfigFile, "--output", "syft-json", "--quiet"}
	}
	return verification.Plan{Steps: []verification.Step{{ID: "scan", Request: r}}}, nil
}

func (a *adapter) Execute(ctx context.Context, plan verification.Plan, runner verification.Runner) error {
	return verification.ExecutePlan(ctx, plan, runner)
}
