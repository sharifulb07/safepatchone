package verification

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"unicode/utf8"

	"safepatch/internal/evidence"
)

// Capabilities describe technical work, independently of product entitlement
// keys. Trusted application composition maps capabilities to access requirements.
type Capability string
type Status = evidence.Status

const (
	StatusPass        = evidence.StatusPass
	StatusFail        = evidence.StatusFail
	StatusWarn        = evidence.StatusWarn
	StatusError       = evidence.StatusError
	StatusSkipped     = evidence.StatusSkipped
	StatusUnavailable = evidence.StatusUnavailable
	StatusUnknown     = evidence.StatusUnknown
	MaxSteps          = evidence.MaxExecutions
	MaxTargetFiles    = 100000
)

var (
	ErrInvalidVerifier     = errors.New("invalid verifier SDK contract")
	ErrVerifierNotFound    = errors.New("verifier not found")
	ErrDuplicateVerifier   = errors.New("duplicate verifier identity")
	ErrInvalidPlan         = errors.New("invalid verifier execution plan")
	ErrVerifierUnavailable = errors.New("required verifier tool or data unavailable")
	ErrVerifierLifecycle   = errors.New("verifier lifecycle failed")
	capabilityPattern      = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	versionPattern         = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

func (c Capability) Valid() bool {
	return len(c) > 0 && len(c) <= 128 && capabilityPattern.MatchString(string(c))
}

// Verifier implementations are trusted Go extensions and must honor contexts.
// Repository contents and tool output are always untrusted. No edition, user,
// subscription, entitlement service, or production approval enters this API.
type Verifier interface {
	ID() string
	Version() string
	Capabilities() []Capability
	Detect(context.Context, Target) (Detection, error)
	Plan(context.Context, PlanRequest) (Plan, error)
	Execute(context.Context, Plan, Runner) error
	Normalize(context.Context, NormalizationRequest) (evidence.Report, error)
}

// Target is a bounded manifest supplied by trusted repository discovery. Detect
// is metadata-only; there is no process, network, or filesystem adapter here.
type Target struct {
	Subject evidence.Subject `json:"subject"`
	Files   []string         `json:"files"`
	// Metadata contains bounded untrusted manifest text from the same immutable
	// snapshot as Files. It is not executable configuration or authorization.
	Metadata map[string]string `json:"metadata,omitempty"`
}

func (t Target) Validate() error {
	if t.Subject.Validate() != nil || len(t.Files) > MaxTargetFiles {
		return ErrInvalidVerifier
	}
	size := 0
	seen := map[string]bool{}
	for _, name := range t.Files {
		size += len(name)
		if !evidence.ValidPath(name) || seen[name] || size > 8*1024*1024 {
			return ErrInvalidVerifier
		}
		seen[name] = true
	}
	if len(t.Metadata) > 256 {
		return ErrInvalidVerifier
	}
	size = 0
	for name, value := range t.Metadata {
		size += len(value)
		if !seen[name] || !utf8.ValidString(value) || size > 1024*1024 {
			return ErrInvalidVerifier
		}
	}
	return nil
}

type DetectionState string

const (
	Detected             DetectionState = "DETECTED"
	NotApplicable        DetectionState = "NOT_APPLICABLE"
	DetectionUnavailable DetectionState = "UNAVAILABLE"
	DetectionUnknown     DetectionState = "UNKNOWN"
)

type Detection struct {
	State        DetectionState `json:"state"`
	Capabilities []Capability   `json:"capabilities"`
}
type PlanRequest struct {
	Target     Target
	Capability Capability
	// BaseExecution pins trusted source, environment, execution mode and limits.
	// Plan may select a tool/argv/working directory, but cannot change this policy.
	BaseExecution ExecutionRequest
}
type Step struct {
	ID      string           `json:"id"`
	Request ExecutionRequest `json:"request"`
}
type Plan struct {
	Steps []Step `json:"steps"`
}
type StepResult struct {
	StepID string
	Result ExecutionResult
}
type NormalizationRequest struct {
	Target     Target
	Capability Capability
	Results    []StepResult
}

// ExecutePlan is the standard sequential implementation of Verifier.Execute.
// Nonzero scanner exits may encode findings and still require normalization.
func ExecutePlan(ctx context.Context, plan Plan, runner Runner) error {
	if ctx == nil || runner == nil || len(plan.Steps) == 0 || len(plan.Steps) > MaxSteps {
		return ErrInvalidPlan
	}
	for _, step := range plan.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if result, err := runner.Run(ctx, step.Request); err != nil && (result.Status != Failed || !errors.Is(err, ErrExecutionFailed) || errors.Is(err, ErrCleanupFailed)) {
			return err
		}
	}
	return nil
}

type Descriptor struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	Capabilities []Capability `json:"capabilities"`
}
type registration struct {
	descriptor Descriptor
	verifier   Verifier
}
type Registry struct{ entries map[string]registration }

// NewRegistry freezes capability discovery. Duplicate IDs cannot silently
// replace an implementation/version. Build a new registry for a release change.
func NewRegistry(verifiers ...Verifier) (registry *Registry, err error) {
	defer func() {
		if recover() != nil {
			registry = nil
			err = ErrInvalidVerifier
		}
	}()
	if len(verifiers) > 1024 {
		return nil, ErrInvalidVerifier
	}
	r := &Registry{entries: map[string]registration{}}
	for _, v := range verifiers {
		if v == nil {
			return nil, ErrInvalidVerifier
		}
		capabilities := v.Capabilities()
		if len(capabilities) == 0 || len(capabilities) > 64 {
			return nil, ErrInvalidVerifier
		}
		d := Descriptor{ID: v.ID(), Version: v.Version(), Capabilities: append([]Capability{}, capabilities...)}
		if !evidence.ValidIdentifier(d.ID) || len(d.Version) > 128 || !versionPattern.MatchString(d.Version) || len(d.Capabilities) == 0 || len(d.Capabilities) > 64 {
			return nil, ErrInvalidVerifier
		}
		slices.Sort(d.Capabilities)
		for i, c := range d.Capabilities {
			if !c.Valid() || i > 0 && d.Capabilities[i-1] == c {
				return nil, ErrInvalidVerifier
			}
		}
		if _, exists := r.entries[d.ID]; exists {
			return nil, ErrDuplicateVerifier
		}
		r.entries[d.ID] = registration{d, v}
	}
	return r, nil
}
func (r *Registry) Discover(capability Capability) []Descriptor {
	result := []Descriptor{}
	if r == nil || !capability.Valid() {
		return result
	}
	for _, entry := range r.entries {
		if slices.Contains(entry.descriptor.Capabilities, capability) {
			d := entry.descriptor
			d.Capabilities = slices.Clone(d.Capabilities)
			result = append(result, d)
		}
	}
	slices.SortFunc(result, func(a, b Descriptor) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result
}
