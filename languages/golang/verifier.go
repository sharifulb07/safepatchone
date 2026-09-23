package golang

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

const (
	ID                                  = "go"
	Version                             = "1.0.0"
	Build       verification.Capability = "go.build"
	Test        verification.Capability = "go.test"
	Race        verification.Capability = "go.test.race"
	Vet         verification.Capability = "go.vet"
	Staticcheck verification.Capability = "go.staticcheck"
	Vulncheck   verification.Capability = "go.govulncheck"
	Fuzz        verification.Capability = "go.fuzz"
	Benchmark   verification.Capability = "go.benchmark"
)

type FuzzTarget struct{ Module, Package, Function string }
type Config struct {
	GoTool, StaticcheckTool, GovulncheckTool string
	// These are trusted, explicit execution budgets, not edition defaults.
	Parallelism         int
	FuzzSeconds         int
	FuzzTargets         []FuzzTarget
	BenchmarkIterations int
	CGOEnabled          bool
	// Optional private cache provisioned by trusted orchestration. Never share a
	// writable build cache across tenants. Empty uses the Runner's private HOME.
	BuildCache string
	// Read-only vulnerability database provisioned by the operator, outside
	// repository control. Only local file URLs are accepted; never online fetch.
	VulnerabilityDB string
}
type Verifier struct{ config Config }

var fuzzName = regexp.MustCompile(`^Fuzz[A-Za-z0-9_]+$`)

func New(config Config) (*Verifier, error) {
	if !verification.ValidToolName(config.GoTool) || config.Parallelism < 1 || config.Parallelism > 1024 || config.FuzzSeconds < 0 || config.FuzzSeconds > 86400 || config.BenchmarkIterations < 0 || len(config.FuzzTargets) > verification.MaxSteps {
		return nil, verification.ErrInvalidVerifier
	}
	for _, tool := range []string{config.StaticcheckTool, config.GovulncheckTool} {
		if tool != "" && !verification.ValidToolName(tool) {
			return nil, verification.ErrInvalidVerifier
		}
	}
	if config.VulnerabilityDB != "" {
		u, err := url.Parse(config.VulnerabilityDB)
		if err != nil || u.Scheme != "file" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "..") || strings.HasPrefix(u.Path, "/workspace") || strings.HasPrefix(u.Path, "/input") {
			return nil, verification.ErrInvalidVerifier
		}
	}
	if strings.ContainsAny(config.BuildCache, "\x00\r\n") || config.BuildCache != "" && !filepath.IsAbs(config.BuildCache) && !strings.HasPrefix(config.BuildCache, "/") {
		return nil, verification.ErrInvalidVerifier
	}
	for _, target := range config.FuzzTargets {
		if !verification.ValidWorkingDirectory(target.Module) || !verification.ValidWorkingDirectory(target.Package) || !fuzzName.MatchString(target.Function) || len(target.Function) > 128 || config.FuzzSeconds == 0 {
			return nil, verification.ErrInvalidVerifier
		}
	}
	config.FuzzTargets = slices.Clone(config.FuzzTargets)
	return &Verifier{config}, nil
}
func (*Verifier) ID() string      { return ID }
func (*Verifier) Version() string { return Version }
func (*Verifier) Capabilities() []verification.Capability {
	return []verification.Capability{Build, Test, Race, Vet, Staticcheck, Vulncheck, Fuzz, Benchmark}
}

// Environment is trusted policy to put in PlanRequest.BaseExecution, and its
// keys must be allowed by the Runner. Requests cannot override it in Plan.
func (v *Verifier) Environment() map[string]string {
	cgo := "0"
	if v.config.CGOEnabled {
		cgo = "1"
	}
	env := map[string]string{"GOENV": "off", "GOFLAGS": "", "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "", "CGO_ENABLED": cgo, "GOMAXPROCS": strconv.Itoa(v.config.Parallelism)}
	if v.config.BuildCache != "" {
		env["GOCACHE"] = v.config.BuildCache
	}
	return env
}
func (v *Verifier) Detect(ctx context.Context, target verification.Target) (verification.Detection, error) {
	if ctx == nil {
		return verification.Detection{}, verification.ErrInvalidVerifier
	}
	if err := ctx.Err(); err != nil {
		return verification.Detection{}, err
	}
	p, err := discover(target)
	if err != nil {
		return verification.Detection{}, err
	}
	if len(p.modules) == 0 {
		return verification.Detection{State: verification.NotApplicable}, nil
	}
	return verification.Detection{State: verification.Detected, Capabilities: v.Capabilities()}, nil
}
func (v *Verifier) Plan(ctx context.Context, input verification.PlanRequest) (verification.Plan, error) {
	var plan verification.Plan
	if ctx == nil || v == nil {
		return plan, verification.ErrInvalidVerifier
	}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	p, err := discover(input.Target)
	if err != nil || len(p.modules) == 0 {
		return plan, ErrProject
	}
	if !maps.Equal(input.BaseExecution.Environment, v.Environment()) {
		return plan, verification.ErrInvalidPlan
	}
	tool := v.config.GoTool
	switch input.Capability {
	case Staticcheck:
		tool = v.config.StaticcheckTool
	case Vulncheck:
		tool = v.config.GovulncheckTool
		if v.config.VulnerabilityDB == "" {
			return plan, verification.ErrVerifierUnavailable
		}
	case Race:
		if !v.config.CGOEnabled {
			return plan, verification.ErrVerifierUnavailable
		}
	case Build, Test, Vet, Fuzz, Benchmark:
	default:
		return plan, verification.ErrInvalidPlan
	}
	if tool == "" {
		return plan, verification.ErrVerifierUnavailable
	}
	parallel := strconv.Itoa(v.config.Parallelism)
	add := func(dir string, args []string) {
		r := input.BaseExecution
		r.Executable = tool
		r.WorkingDirectory = dir
		r.Arguments = args
		plan.Steps = append(plan.Steps, verification.Step{ID: fmt.Sprintf("module_%03d", len(plan.Steps)+1), Request: r})
	}
	if input.Capability == Fuzz {
		if len(v.config.FuzzTargets) == 0 || time.Duration(v.config.FuzzSeconds)*time.Duration(len(v.config.FuzzTargets))*time.Second >= input.BaseExecution.Limits.Timeout {
			return plan, verification.ErrInvalidPlan
		}
		seen := map[string]bool{}
		for _, target := range v.config.FuzzTargets {
			dir := target.Module
			if dir == "" {
				dir = "."
			}
			if !slices.Contains(p.modules, dir) {
				return plan, ErrProject
			}
			key := dir + "/" + target.Package + "/" + target.Function
			if seen[key] {
				return plan, ErrProject
			}
			seen[key] = true
			pkg := "."
			if target.Package != "" && target.Package != "." {
				pkg = "./" + target.Package
			}
			add(dir, []string{"test", "-json", "-count=1", "-vet=off", "-p=" + parallel, "-parallel=" + parallel, "-timeout=" + input.BaseExecution.Limits.Timeout.String(), "-run=^$", "-fuzz=^" + target.Function + "$", "-fuzztime=" + strconv.Itoa(v.config.FuzzSeconds) + "s", pkg})
		}
		return plan, nil
	}
	for _, dir := range p.modules {
		var args []string
		switch input.Capability {
		case Build:
			args = []string{"build", "-json", "-p=" + parallel, "./..."}
		case Vet:
			args = []string{"vet", "-json", "-p=" + parallel, "./..."}
		case Staticcheck:
			args = []string{"-f=json", "-checks=all", "-fail=all", "-show-ignored", "./..."}
		case Vulncheck:
			args = []string{"-json", "-scan=symbol", "-db=" + v.config.VulnerabilityDB, "./..."}
		case Test, Race, Benchmark:
			args = []string{"test", "-json", "-count=1", "-vet=off", "-p=" + parallel, "-parallel=" + parallel, "-timeout=" + input.BaseExecution.Limits.Timeout.String()}
			if input.Capability == Race {
				args = append(args, "-race")
			}
			if input.Capability == Benchmark {
				if v.config.BenchmarkIterations < 1 {
					return plan, verification.ErrInvalidPlan
				}
				args = append(args, "-run=^$", "-bench=.", "-benchmem", "-benchtime="+strconv.Itoa(v.config.BenchmarkIterations)+"x")
			}
			args = append(args, "./...")
		}
		add(dir, args)
	}
	return plan, nil
}
func (*Verifier) Execute(ctx context.Context, plan verification.Plan, runner verification.Runner) error {
	return verification.ExecutePlan(ctx, plan, runner)
}

func (v *Verifier) Normalize(ctx context.Context, input verification.NormalizationRequest) (evidence.Report, error) {
	if ctx == nil || v == nil || len(input.Results) == 0 {
		return evidence.Report{}, verification.ErrInvalidVerifier
	}
	p, projectErr := discover(input.Target)
	if projectErr != nil {
		return evidence.Report{}, projectErr
	}
	report := evidence.Report{Status: evidence.StatusPass, Summary: string(input.Capability) + " completed", Findings: []evidence.Finding{}}
	for index, step := range input.Results {
		if err := ctx.Err(); err != nil {
			return evidence.Report{}, err
		}
		r := step.Result
		if r.StdoutTruncated || r.StderrTruncated || (r.Status != verification.Completed && r.Status != verification.Failed) {
			return evidence.Report{Status: evidence.StatusUnknown, Summary: "Incomplete Go execution"}, nil
		}
		partial, err := v.normalize(input.Capability, r)
		if err != nil {
			return evidence.Report{}, err
		}
		module := "."
		if input.Capability == Fuzz {
			if index >= len(v.config.FuzzTargets) {
				return evidence.Report{}, verification.ErrInvalidVerifier
			}
			module = v.config.FuzzTargets[index].Module
		} else {
			if index >= len(p.modules) {
				return evidence.Report{}, verification.ErrInvalidVerifier
			}
			module = p.modules[index]
		}
		for i := range partial.Findings {
			if partial.Findings[i].Path != "" {
				partial.Findings[i].Path = path.Join(module, partial.Findings[i].Path)
			}
		}
		if rank(partial.Status) > rank(report.Status) {
			report.Status = partial.Status
		}
		report.Findings = append(report.Findings, partial.Findings...)
		if len(report.Findings) > evidence.MaxFindings {
			return evidence.Report{}, evidence.ErrInvalidEvidence
		}
	}
	report.Summary = fmt.Sprintf("%s: %s across %d execution steps", input.Capability, report.Status, len(input.Results))
	return report.Canonical()
}
func rank(s evidence.Status) int {
	switch s {
	case evidence.StatusPass:
		return 0
	case evidence.StatusSkipped:
		return 1
	case evidence.StatusWarn:
		return 2
	case evidence.StatusFail:
		return 3
	case evidence.StatusUnavailable:
		return 4
	case evidence.StatusError:
		return 5
	default:
		return 6
	}
}

func location(file string) (string, int) {
	parts := strings.Split(file, ":")
	line := 0
	if len(parts) > 2 {
		if _, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
			parts = parts[:len(parts)-1]
		}
	}
	if len(parts) > 1 {
		line, _ = strconv.Atoi(parts[len(parts)-1])
		parts = parts[:len(parts)-1]
	}
	file = strings.TrimPrefix(strings.Join(parts, ":"), "./")
	if !evidence.ValidPath(file) {
		return "", 0
	}
	return path.Clean(file), max(line, 0)
}
