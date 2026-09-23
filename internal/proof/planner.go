package proof

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"

	"safepatch/internal/evidence"
	"safepatch/internal/tenant"
	"safepatch/internal/verification"
)

const (
	PlanSchema     = "safepatch.proofplan.v1"
	MaxObligations = 128
	MaxProviders   = 256
	MaxResources   = 16
)

type Level string

const (
	L0 Level = "L0"
	L1 Level = "L1"
	L2 Level = "L2"
	L3 Level = "L3"
	L4 Level = "L4"
	L5 Level = "L5"
	L6 Level = "L6"
	L7 Level = "L7"
	L8 Level = "L8"
)

func Levels() []Level     { return []Level{L0, L1, L2, L3, L4, L5, L6, L7, L8} }
func (l Level) Rank() int { return slices.Index(Levels(), l) }
func (l Level) Name() string {
	names := []string{"syntax", "build", "tests", "security", "semantic", "behavioral", "adversarial", "runtime", "formal"}
	if r := l.Rank(); r >= 0 {
		return names[r]
	}
	return ""
}

var (
	ErrInvalidPlanner = errors.New("invalid proof planner configuration")
	ErrInvalidRequest = errors.New("invalid proof planning request")
	ErrInvalidPlan    = errors.New("invalid or uninitialized proof plan")
)

// Obligations are trusted policy configuration, not claims from scanner output.
// Every profile covers all nine levels. Target selection includes every earlier
// obligation; capability access must never silently lower the requested level.
type Obligation struct {
	ID         string                  `json:"id"`
	Level      Level                   `json:"level"`
	Capability verification.Capability `json:"capability"`
}

func DefaultObligations() []Obligation {
	out := make([]Obligation, 0, 9)
	for _, l := range Levels() {
		out = append(out, Obligation{ID: l.Name(), Level: l, Capability: verification.Capability("proof." + l.Name())})
	}
	return out
}

// Cost is a trusted upper bound for additive resource consumption by one task.
// It has no pricing/edition meaning. Scheduling capacity (e.g. concurrency) is
// checked at dispatch, rather than added as if it were consumable work.
type Cost struct {
	Resource string `json:"resource"`
	Amount   int64  `json:"amount"`
}
type Budget struct {
	Resource  string
	Remaining int64
}
type Provider struct {
	ID         string                  `json:"id"`
	Version    string                  `json:"version"`
	Capability verification.Capability `json:"capability"`
	Priority   uint16                  `json:"priority"`
	Costs      []Cost                  `json:"costs"`
}
type Availability string

const (
	Available   Availability = "AVAILABLE"
	Disabled    Availability = "DISABLED"
	Unavailable Availability = "UNAVAILABLE"
	Unknown     Availability = "UNKNOWN"
)

func (a Availability) Valid() bool {
	return slices.Contains([]Availability{Available, Disabled, Unavailable, Unknown}, a)
}

type ProviderState struct {
	ID           string
	Availability Availability
}
type Request struct {
	TenantID  string
	Subject   evidence.Subject
	Target    Level
	Providers []ProviderState
	Budgets   []Budget
}
type PlanState string

const (
	Ready   PlanState = "READY"
	Blocked PlanState = "BLOCKED"
)

type ResolutionCode string

const (
	Selected            ResolutionCode = "SELECTED"
	CapabilityDisabled  ResolutionCode = "CAPABILITY_DISABLED"
	ProviderUnavailable ResolutionCode = "PROVIDER_UNAVAILABLE"
	ProviderUnknown     ResolutionCode = "PROVIDER_UNKNOWN"
	BudgetExceeded      ResolutionCode = "BUDGET_EXCEEDED"
)

type Resolution struct {
	Obligation Obligation     `json:"obligation"`
	Code       ResolutionCode `json:"code"`
	StepID     string         `json:"step_id,omitempty"`
}
type PlannedStep struct {
	ID       string   `json:"id"`
	Provider Provider `json:"provider"`
}
type PlanFields struct {
	Schema            string           `json:"schema"`
	PlannerVersion    string           `json:"planner_version"`
	ConfigurationHash string           `json:"configuration_hash"`
	TenantID          string           `json:"tenant_id"`
	Subject           evidence.Subject `json:"subject"`
	Target            Level            `json:"target"`
	State             PlanState        `json:"state"`
	Steps             []PlannedStep    `json:"steps"`
	Resolutions       []Resolution     `json:"resolutions"`
	Totals            []Cost           `json:"totals"`
}

// A plan is a sealed recommendation, never a quota reservation, verification
// result, achieved proof level, production approval, or durable execution lease.
type Plan struct{ canonical string }

func (p Plan) Fields() (PlanFields, error) {
	var f PlanFields
	if p.canonical == "" || json.Unmarshal([]byte(p.canonical), &f) != nil {
		return f, ErrInvalidPlan
	}
	return f, nil
}
func (p Plan) MarshalJSON() ([]byte, error) {
	if p.canonical == "" {
		return nil, ErrInvalidPlan
	}
	return []byte(p.canonical), nil
}
func (*Plan) UnmarshalJSON([]byte) error { return ErrInvalidPlan }
func (p Plan) Hash() string {
	if p.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(PlanSchema + "\n" + p.canonical))
}

// ProofPlanner freezes one edition-independent profile and provider catalog.
// Selection is bounded, deterministic priority-first greedy planning, not global
// cost optimization. Providers are reused when obligations need the same task.
type ProofPlanner struct {
	version, hash string
	obligations   []Obligation
	providers     []Provider
}

func NewProofPlanner(version string, obligations []Obligation, providers []Provider) (*ProofPlanner, error) {
	if !evidence.ValidIdentifier(version) || len(obligations) == 0 || len(obligations) > MaxObligations || len(providers) > MaxProviders {
		return nil, ErrInvalidPlanner
	}
	p := &ProofPlanner{version: version, obligations: slices.Clone(obligations), providers: make([]Provider, len(providers))}
	ids := map[string]bool{}
	levels := map[Level]bool{}
	for _, o := range p.obligations {
		if !evidence.ValidIdentifier(o.ID) || ids[o.ID] || o.Level.Rank() < 0 || !o.Capability.Valid() {
			return nil, ErrInvalidPlanner
		}
		ids[o.ID] = true
		levels[o.Level] = true
	}
	if len(levels) != len(Levels()) {
		return nil, ErrInvalidPlanner
	}
	slices.SortFunc(p.obligations, func(a, b Obligation) int {
		if a.Level != b.Level {
			return a.Level.Rank() - b.Level.Rank()
		}
		return compare(a.ID, b.ID)
	})
	ids = map[string]bool{}
	allResources := map[string]bool{}
	for i, v := range providers {
		if !evidence.ValidIdentifier(v.ID) || !evidence.ValidIdentifier(v.Version) || !v.Capability.Valid() || ids[v.ID] || len(v.Costs) > MaxResources {
			return nil, ErrInvalidPlanner
		}
		ids[v.ID] = true
		v.Costs = slices.Clone(v.Costs)
		if v.Costs == nil {
			v.Costs = []Cost{}
		}
		resources := map[string]bool{}
		for _, c := range v.Costs {
			if !verification.Capability(c.Resource).Valid() || c.Amount <= 0 || resources[c.Resource] {
				return nil, ErrInvalidPlanner
			}
			resources[c.Resource] = true
			allResources[c.Resource] = true
		}
		slices.SortFunc(v.Costs, func(a, b Cost) int { return compare(a.Resource, b.Resource) })
		p.providers[i] = v
	}
	if len(allResources) > MaxResources {
		return nil, ErrInvalidPlanner
	}
	slices.SortFunc(p.providers, func(a, b Provider) int {
		if a.Priority != b.Priority {
			return int(a.Priority) - int(b.Priority)
		}
		return compare(a.ID, b.ID)
	})
	raw, _ := json.Marshal(struct {
		Version     string
		Obligations []Obligation
		Providers   []Provider
	}{p.version, p.obligations, p.providers})
	p.hash = evidence.Digest(raw)
	return p, nil
}
func compare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
func (p *ProofPlanner) Providers() []Provider {
	if p == nil {
		return nil
	}
	out := slices.Clone(p.providers)
	for i := range out {
		out[i].Costs = slices.Clone(out[i].Costs)
	}
	return out
}
func (p *ProofPlanner) ConfigurationHash() string {
	if p == nil {
		return ""
	}
	return p.hash
}

// ProvidersFor excludes work above the target before readiness probes run.
func (p *ProofPlanner) ProvidersFor(target Level) []Provider {
	if p == nil || target.Rank() < 0 {
		return nil
	}
	needed := map[verification.Capability]bool{}
	for _, o := range p.obligations {
		if o.Level.Rank() <= target.Rank() {
			needed[o.Capability] = true
		}
	}
	out := []Provider{}
	for _, v := range p.Providers() {
		if needed[v.Capability] {
			out = append(out, v)
		}
	}
	return out
}

func (p *ProofPlanner) Plan(ctx context.Context, r Request) (Plan, error) {
	if p == nil || p.hash == "" || ctx == nil || !tenant.ValidID(r.TenantID) || r.Subject.Validate() != nil || r.Target.Rank() < 0 || len(r.Providers) > MaxProviders || len(r.Budgets) > MaxResources {
		return Plan{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	catalog := map[string]bool{}
	for _, v := range p.providers {
		catalog[v.ID] = true
	}
	states := map[string]Availability{}
	for _, s := range r.Providers {
		if !catalog[s.ID] || states[s.ID] != "" || !s.Availability.Valid() {
			return Plan{}, ErrInvalidRequest
		}
		states[s.ID] = s.Availability
	}
	remaining := map[string]int64{}
	for _, b := range r.Budgets {
		if _, exists := remaining[b.Resource]; exists || !verification.Capability(b.Resource).Valid() || b.Remaining < 0 {
			return Plan{}, ErrInvalidRequest
		}
		remaining[b.Resource] = b.Remaining
	}
	f := PlanFields{Schema: PlanSchema, PlannerVersion: p.version, ConfigurationHash: p.hash, TenantID: r.TenantID, Subject: r.Subject, Target: r.Target, State: Ready, Steps: []PlannedStep{}, Resolutions: []Resolution{}, Totals: []Cost{}}
	chosen := map[verification.Capability]string{}
	totals := map[string]int64{}
	for _, o := range p.obligations {
		if err := ctx.Err(); err != nil {
			return Plan{}, err
		}
		if o.Level.Rank() > r.Target.Rank() {
			continue
		}
		resolution := Resolution{Obligation: o, Code: ProviderUnavailable}
		if id, ok := chosen[o.Capability]; ok {
			resolution.Code = Selected
			resolution.StepID = id
			f.Resolutions = append(f.Resolutions, resolution)
			continue
		}
		budgetDenied, unknown, disabled := false, false, false
		for _, v := range p.providers {
			if v.Capability != o.Capability {
				continue
			}
			switch states[v.ID] {
			case Available:
			case Disabled:
				disabled = true
				continue
			case Unavailable:
				continue
			default:
				unknown = true
				continue
			}
			fits := true
			for _, c := range v.Costs {
				if c.Amount > remaining[c.Resource] || c.Amount > math.MaxInt64-totals[c.Resource] {
					fits = false
					break
				}
			}
			if !fits {
				budgetDenied = true
				continue
			}
			stepID := "step." + evidence.Digest([]byte(v.ID))
			f.Steps = append(f.Steps, PlannedStep{ID: stepID, Provider: v})
			chosen[o.Capability] = stepID
			for _, c := range v.Costs {
				remaining[c.Resource] -= c.Amount
				totals[c.Resource] += c.Amount
			}
			resolution.Code = Selected
			resolution.StepID = stepID
			break
		}
		if resolution.Code != Selected {
			f.State = Blocked
			switch {
			case budgetDenied:
				resolution.Code = BudgetExceeded
			case unknown:
				resolution.Code = ProviderUnknown
			case disabled:
				resolution.Code = CapabilityDisabled
			}
		}
		f.Resolutions = append(f.Resolutions, resolution)
	}
	for k, v := range totals {
		f.Totals = append(f.Totals, Cost{Resource: k, Amount: v})
	}
	slices.SortFunc(f.Totals, func(a, b Cost) int { return compare(a.Resource, b.Resource) })
	raw, err := json.Marshal(f)
	if err != nil {
		return Plan{}, ErrInvalidPlan
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	return Plan{canonical: string(raw)}, nil
}
