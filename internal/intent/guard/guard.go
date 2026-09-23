package guard

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

const Version = "1.0.0"

// Identity and collection completeness come from trusted orchestration. They
// must not be copied from repository content, scanner text or an LLM response.
type Identity struct {
	TenantID     string    `json:"tenant_id"`
	RepositoryID string    `json:"repository_id"`
	ChangeID     string    `json:"change_id"`
	RunID        string    `json:"run_id"`
	CreatedAt    time.Time `json:"created_at"`
}

// SemanticChange is an explicit path-to-component fact. This milestone does not
// infer behavior from prose or introduce a speculative language-specific SIR.
type SemanticChange struct {
	Path      string `json:"path"`
	Component string `json:"component"`
}
type SemanticChanges struct {
	TenantID      string           `json:"tenant_id"`
	RepositoryID  string           `json:"repository_id"`
	ChangeID      string           `json:"change_id"`
	RunID         string           `json:"run_id"`
	DiffHash      string           `json:"diff_hash"`
	Complete      bool             `json:"complete"`
	Deterministic bool             `json:"deterministic"`
	Changes       []SemanticChange `json:"changes"`
}

// ContentHash binds semantic facts and their scope/completeness. It is not a
// signature. Input DTOs must not be concurrently mutated by their owner.
func (s SemanticChanges) ContentHash() (string, error) {
	if !tenant.ValidID(s.TenantID) || !tenant.ValidID(s.RepositoryID) || !evidence.ValidIdentifier(s.ChangeID) || !evidence.ValidIdentifier(s.RunID) || len(s.DiffHash) != 64 || !objectID(s.DiffHash) || len(s.Changes) > MaxChangedPaths {
		return "", ErrInvalidInput
	}
	s.Changes = append([]SemanticChange{}, s.Changes...)
	slices.SortFunc(s.Changes, func(a, b SemanticChange) int { return strings.Compare(a.Path, b.Path) })
	last := ""
	for _, c := range s.Changes {
		if !safePath(c.Path) || !safePath(c.Component) || c.Path == last {
			return "", ErrInvalidInput
		}
		last = c.Path
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", ErrInvalidInput
	}
	return evidence.Digest(append([]byte("safepatch.semantic-changes.v1\n"), raw...)), nil
}

type Input struct {
	Identity        Identity
	Contract        intent.IntentContract
	SemanticChanges SemanticChanges
	GitDiff         GitDiff
	Properties      *spec.Specification
	Evidence        []evidence.Evidence
}

type Assessment struct {
	Status evidence.Status `json:"status"`
	Code   string          `json:"code"`
}
type Drift struct {
	Path      string `json:"path"`
	Component string `json:"component,omitempty"`
	Code      string `json:"code"`
}
type ScopeResult struct {
	Assessment
	Drifts []Drift `json:"drifts"`
}
type PropertyResult struct {
	AssertionID string `json:"assertion_id"`
	Assessment
	SourceEvidenceIDs []string `json:"source_evidence_ids"`
}
type Result struct {
	IntentCompliance    Assessment          `json:"intent_compliance"`
	ScopeDrift          ScopeResult         `json:"scope_drift"`
	ProtectedProperties []PropertyResult    `json:"protected_properties"`
	EvidenceIDs         []string            `json:"evidence_ids"`
	Evidence            []evidence.Evidence `json:"-"`
}

// IntentGuard emits shared immutable Evidence and diagnostics. It never invokes
// a command, reads paths, synthesizes observations or authorizes production.
type IntentGuard struct{}

func (IntentGuard) Evaluate(ctx context.Context, in Input) (Result, error) {
	fail := func(err error) (Result, error) {
		return Result{IntentCompliance: Assessment{evidence.StatusError, "INVALID_INPUT"}, ScopeDrift: ScopeResult{Assessment: Assessment{evidence.StatusError, "INVALID_INPUT"}, Drifts: []Drift{}}, ProtectedProperties: []PropertyResult{}, EvidenceIDs: []string{}, Evidence: []evidence.Evidence{}}, err
	}
	if ctx == nil {
		return fail(ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	id := in.Identity
	if !tenant.ValidID(id.TenantID) || !tenant.ValidID(id.RepositoryID) || !evidence.ValidIdentifier(id.ChangeID) || !evidence.ValidIdentifier(id.RunID) || id.CreatedAt.IsZero() || id.CreatedAt.UTC().Year() < 1 || id.CreatedAt.UTC().Year() > 9999 {
		return fail(ErrInvalidInput)
	}
	id.CreatedAt = id.CreatedAt.UTC().Round(0)
	f, err := in.Contract.Fields()
	if err != nil {
		return fail(err)
	}
	contractHash, _ := in.Contract.ContentHash()
	paths, err := ParseGitDiff(ctx, in.GitDiff.Raw)
	if err != nil {
		return fail(err)
	}
	diffHash := evidence.Digest(in.GitDiff.Raw)
	sem := in.SemanticChanges
	if sem.TenantID != id.TenantID || sem.RepositoryID != id.RepositoryID || sem.ChangeID != id.ChangeID || sem.RunID != id.RunID || sem.DiffHash != diffHash {
		return fail(ErrInvalidInput)
	}
	semHash, err := sem.ContentHash()
	if err != nil {
		return fail(err)
	}
	mapping := map[string]string{}
	for _, c := range sem.Changes {
		if !slices.Contains(paths, c.Path) {
			return fail(ErrInvalidInput)
		}
		mapping[c.Path] = c.Component
	}
	complete := in.GitDiff.Complete && sem.Complete && sem.Deterministic && len(mapping) == len(paths)
	scope := scopeDrift(f, paths, mapping, complete)
	assertions, _ := spec.Assertions(in.Contract)
	rules := map[string]policy.RuleResult{}
	propertyHash := evidence.Digest(nil)
	if in.Properties != nil {
		if in.Properties.ContractHash() != contractHash {
			return fail(ErrInvalidInput)
		}
		propertyHash = in.Properties.ContentHash()
		checked, err := in.Properties.Evaluate(ctx, policy.Input{TenantID: id.TenantID, RepositoryID: id.RepositoryID, ChangeID: id.ChangeID, RunID: id.RunID, AsOf: id.CreatedAt, Criticality: policy.CriticalityUnknown, Evidence: in.Evidence})
		if err != nil {
			return fail(err)
		}
		used := map[string]bool{}
		for _, rule := range checked.Checks.Rules {
			rules[rule.RuleID] = rule
			for _, check := range rule.Checks {
				for _, id := range check.EvidenceIDs {
					used[id] = true
				}
			}
		}
		for _, node := range in.Evidence {
			f, _ := node.Fields() // SpecForge already validated every envelope.
			if used[f.EvidenceID] && (f.InputHashes["git_diff"] != diffHash || f.InputHashes["semantic_changes"] != semHash) {
				return fail(ErrInvalidInput)
			}
		}
	} else if len(in.Evidence) != 0 {
		return fail(ErrInvalidInput)
	}
	r := Result{IntentCompliance: Assessment{evidence.StatusPass, "SATISFIED"}, ScopeDrift: scope, ProtectedProperties: []PropertyResult{}, EvidenceIDs: []string{}, Evidence: []evidence.Evidence{}}
	if !complete || len(assertions) == 0 {
		r.IntentCompliance = Assessment{evidence.StatusUnknown, "INCOMPLETE_INPUT"}
	}
	for _, a := range assertions {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		p := PropertyResult{AssertionID: a.ID, Assessment: Assessment{evidence.StatusUnknown, "MISSING_PROPERTY_CHECK"}, SourceEvidenceIDs: []string{}}
		if rule, ok := rules[a.ID]; ok {
			p.Assessment = Assessment{rule.Status, "PROPERTY_CHECK"}
			for _, check := range rule.Checks {
				p.SourceEvidenceIDs = append(p.SourceEvidenceIDs, check.EvidenceIDs...)
			}
			slices.Sort(p.SourceEvidenceIDs)
			p.SourceEvidenceIDs = slices.Compact(p.SourceEvidenceIDs)
		}
		if a.Field == "allowed_components" || a.Field == "forbidden_components" {
			if _, ok := rules[a.ID]; ok {
				p.Status = combine(p.Status, scope.Status)
			} else {
				p.Assessment = scope.Assessment
			}
		}
		if a.Field == "protected_properties" {
			r.ProtectedProperties = append(r.ProtectedProperties, p)
		}
		r.IntentCompliance.Status = combine(r.IntentCompliance.Status, p.Status)
	}
	// Scope failure is never hidden by a passing subset of property checks.
	if scope.Status != evidence.StatusSkipped {
		r.IntentCompliance.Status = combine(r.IntentCompliance.Status, scope.Status)
	}
	switch r.IntentCompliance.Status {
	case evidence.StatusFail:
		r.IntentCompliance.Code = "INTENT_VIOLATION"
	case evidence.StatusUnknown:
		r.IntentCompliance.Code = "INSUFFICIENT_EVIDENCE"
	}
	evidenceHashes := []string{}
	for _, node := range in.Evidence {
		f, _ := node.Fields()
		evidenceHashes = append(evidenceHashes, f.ContentHash)
	}
	slices.Sort(evidenceHashes)
	inputHashes := map[string]string{"intent_contract": contractHash, "git_diff": diffHash, "semantic_changes": semHash, "specforge_properties": propertyHash, "source_evidence": evidence.Digest([]byte(strings.Join(evidenceHashes, "\n")))}
	// Completeness is a separate trusted input, and must be bound to output IDs.
	if in.GitDiff.Complete {
		inputHashes["diff_collection"] = evidence.Digest([]byte("complete"))
	} else {
		inputHashes["diff_collection"] = evidence.Digest([]byte("partial"))
	}
	for _, item := range []struct {
		kind    string
		status  evidence.Status
		payload any
	}{
		{"intent.compliance", r.IntentCompliance.Status, r.IntentCompliance},
		{"intent.scope_drift", scope.Status, scope},
		{"intent.protected_properties", propertyStatus(r.ProtectedProperties), r.ProtectedProperties},
	} {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		node, err := makeEvidence(id, item.kind, item.status, item.payload, inputHashes)
		if err != nil {
			return fail(err)
		}
		fields, _ := node.Fields()
		r.EvidenceIDs = append(r.EvidenceIDs, fields.EvidenceID)
		r.Evidence = append(r.Evidence, node)
	}
	return r, nil
}

func scopeDrift(f intent.IntentContractFields, paths []string, mapping map[string]string, complete bool) ScopeResult {
	r := ScopeResult{Assessment: Assessment{evidence.StatusPass, "WITHIN_DECLARED_SCOPE"}, Drifts: []Drift{}}
	if !complete {
		r.Assessment = Assessment{evidence.StatusUnknown, "INCOMPLETE_SCOPE"}
	} else if len(f.AllowedComponents) == 0 && len(f.ForbiddenComponents) == 0 {
		r.Assessment = Assessment{evidence.StatusSkipped, "NO_SCOPE_CONSTRAINTS"}
	}
	for _, path := range paths {
		component, mapped := mapping[path]
		code := ""
		if slices.Contains(f.ForbiddenComponents, path) || mapped && slices.Contains(f.ForbiddenComponents, component) {
			code = "FORBIDDEN_COMPONENT"
		} else if mapped && len(f.AllowedComponents) > 0 && !slices.Contains(f.AllowedComponents, component) {
			code = "OUTSIDE_ALLOWED_COMPONENTS"
		}
		if code != "" {
			r.Drifts = append(r.Drifts, Drift{path, component, code})
			r.Assessment = Assessment{evidence.StatusFail, "SCOPE_DRIFT"}
		}
	}
	return r
}

func combine(a, b evidence.Status) evidence.Status {
	if a == evidence.StatusFail || b == evidence.StatusFail || a == evidence.StatusWarn || b == evidence.StatusWarn {
		return evidence.StatusFail
	}
	if a != evidence.StatusPass || b != evidence.StatusPass {
		return evidence.StatusUnknown
	}
	return evidence.StatusPass
}
func propertyStatus(results []PropertyResult) evidence.Status {
	if len(results) == 0 {
		return evidence.StatusSkipped
	}
	status := evidence.StatusPass
	for _, p := range results {
		status = combine(status, p.Status)
	}
	return status
}
func makeEvidence(id Identity, kind string, status evidence.Status, payload any, hashes map[string]string) (evidence.Evidence, error) {
	raw, err := json.Marshal(struct {
		RepositoryID string `json:"repository_id"`
		Result       any    `json:"result"`
	}{id.RepositoryID, payload})
	if err != nil || len(raw) > evidence.MaxPayloadBytes {
		return evidence.Evidence{}, ErrInvalidInput
	}
	seed, err := json.Marshal(struct {
		Identity Identity
		Kind     string
		Hashes   map[string]string
		Payload  json.RawMessage
	}{id, kind, hashes, raw})
	if err != nil {
		return evidence.Evidence{}, ErrInvalidInput
	}
	severity := evidence.SeverityInfo
	if status == evidence.StatusFail {
		severity = evidence.SeverityError
	}
	confidence := float64(0)
	if status == evidence.StatusPass || status == evidence.StatusFail {
		confidence = 1
	}
	return evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: "ig." + evidence.Digest(seed), TenantID: id.TenantID, ChangeID: id.ChangeID, RunID: id.RunID, Producer: "intentguard", ProducerVersion: Version, Category: "intent", Type: kind, Deterministic: true, Status: status, Severity: severity, Confidence: confidence, Summary: "Deterministic IntentGuard assessment", Payload: raw, InputHashes: hashes, CreatedAt: id.CreatedAt})
}
