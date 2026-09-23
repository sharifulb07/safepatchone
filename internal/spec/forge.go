package spec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/tenant"
)

const (
	SpecificationSchema   = "safepatch.specification.v1"
	ForgeVersion          = "1.0.0"
	MaxSpecificationBytes = 512 * 1024
	MaxBindings           = policy.MaxRules
	ContractInputHash     = "intent_contract"
)

var (
	ErrInvalidSpecification   = errors.New("invalid executable specification")
	ErrInvalidBinding         = errors.New("invalid specification evidence binding")
	ErrImmutableSpecification = errors.New("decode immutable specification using DecodeSpecification")
)

// Binding is trusted configuration linking an exact assertion to a reviewed
// check. A forbidden-behavior binding must test its absence: FAIL is never
// inverted into success. Selectors are not commands or natural-language code.
type Binding struct {
	AssertionID string                     `json:"assertion_id"`
	Evidence    policy.EvidenceRequirement `json:"evidence"`
}

type specificationDocument struct {
	Schema          string          `json:"schema"`
	ProducerVersion string          `json:"producer_version"`
	TenantID        string          `json:"tenant_id"`
	RepositoryID    string          `json:"repository_id"`
	Contract        json.RawMessage `json:"contract"`
	Bindings        []Binding       `json:"bindings"`
}

// Specification seals the complete contract, including unbound declarations.
// Serialization preserves generation inputs; decoding regenerates the same
// executable policy rather than accepting injected rules or approval claims.
type Specification struct {
	canonical    string
	contract     intent.IntentContract
	checks       policy.Policy
	repositoryID string
}

func (s Specification) MarshalJSON() ([]byte, error) {
	if s.canonical == "" {
		return nil, ErrInvalidSpecification
	}
	return []byte(s.canonical), nil
}
func (*Specification) UnmarshalJSON([]byte) error { return ErrImmutableSpecification }
func (s Specification) ContentHash() string {
	if s.canonical == "" {
		return ""
	}
	return evidence.Digest([]byte(SpecificationSchema + "\n" + s.canonical))
}

type SpecForge struct{}

// ContractHash exposes the sealed input identity for downstream shared checks.
// It does not disclose mutable fields or establish artifact provenance.
func (s Specification) ContractHash() string {
	hash, _ := s.contract.ContentHash()
	return hash
}

// Generate compiles explicit bindings into the existing policy evaluator. At
// least one binding is required. Unbound declarations remain visible and prevent
// PASS; generation cannot infer test semantics from prose or a producer name.
func (SpecForge) Generate(ctx context.Context, c intent.IntentContract, scope tenant.Scope, bindings []Binding) (Specification, error) {
	if err := contextError(ctx); err != nil {
		return Specification{}, err
	}
	if scope.Validate() != nil {
		return Specification{}, ErrInvalidSpecification
	}
	assertions, err := Assertions(c)
	if err != nil {
		return Specification{}, err
	}
	if len(bindings) == 0 || len(bindings) > MaxBindings {
		return Specification{}, ErrInvalidBinding
	}
	known := map[string]bool{}
	for _, a := range assertions {
		known[a.ID] = true
	}
	bound := map[string]bool{}
	bindings = slices.Clone(bindings)
	slices.SortFunc(bindings, func(a, b Binding) int { return bytes.Compare([]byte(a.AssertionID), []byte(b.AssertionID)) })
	hash, _ := c.ContentHash()
	d := policy.Document{Schema: policy.Schema, PolicyID: hash, TenantID: scope.OrganizationID, Version: ForgeVersion, Rules: []policy.Rule{}}
	for _, b := range bindings {
		if err := contextError(ctx); err != nil {
			return Specification{}, err
		}
		if !known[b.AssertionID] || bound[b.AssertionID] {
			return Specification{}, ErrInvalidBinding
		}
		bound[b.AssertionID] = true
		// Executable assertion checks must be deterministic. Caller configuration
		// cannot downgrade this requirement to let an LLM supply a passing check.
		b.Evidence.Deterministic = true
		d.Rules = append(d.Rules, policy.Rule{ID: b.AssertionID, When: policy.Selector{}, Require: policy.Requirements{Evidence: []policy.EvidenceRequirement{b.Evidence}}})
	}
	checks, err := policy.New(d)
	if err != nil {
		return Specification{}, ErrInvalidBinding
	}
	for i := range bindings {
		bindings[i].Evidence.Deterministic = true
	}
	rawContract, _ := c.CanonicalJSON()
	raw, err := json.Marshal(specificationDocument{SpecificationSchema, ForgeVersion, scope.OrganizationID, scope.ResourceID, rawContract, bindings})
	if err != nil || len(raw) > MaxSpecificationBytes {
		return Specification{}, ErrInvalidSpecification
	}
	if err := contextError(ctx); err != nil {
		return Specification{}, err
	}
	return Specification{canonical: string(raw), contract: c, checks: checks, repositoryID: scope.ResourceID}, nil
}

func DecodeSpecification(ctx context.Context, raw []byte) (Specification, error) {
	if err := contextError(ctx); err != nil {
		return Specification{}, err
	}
	checked, err := jsonobject.Normalize(raw, MaxSpecificationBytes)
	if err != nil {
		return Specification{}, ErrInvalidSpecification
	}
	f, err := exactObject(checked, "schema", "producer_version", "tenant_id", "repository_id", "contract", "bindings")
	if err != nil {
		return Specification{}, err
	}
	var d specificationDocument
	if json.Unmarshal(checked, &d) != nil || d.Schema != SpecificationSchema || d.ProducerVersion != ForgeVersion {
		return Specification{}, ErrInvalidSpecification
	}
	var entries []json.RawMessage
	if json.Unmarshal(f["bindings"], &entries) != nil || len(entries) > MaxBindings {
		return Specification{}, ErrInvalidSpecification
	}
	for _, entry := range entries {
		b, err := exactObject(entry, "assertion_id", "evidence")
		if err != nil {
			return Specification{}, err
		}
		// Reuse strict policy decoding to validate optional selector fields,
		// including duplicate/case aliases and null values.
		probe := []byte(`{"schema":"safepatch.policy.v1","policy_id":"probe","tenant_id":"probe","version":"1","rules":[{"id":"probe","when":{},"require":{"evidence":[`)
		probe = append(probe, b["evidence"]...)
		probe = append(probe, []byte(`]}}]}`)...)
		if _, err := policy.Decode(probe); err != nil {
			return Specification{}, ErrInvalidBinding
		}
	}
	c, err := intent.DecodeIntentContract(d.Contract)
	if err != nil {
		return Specification{}, err
	}
	return (SpecForge{}).Generate(ctx, c, tenant.Scope{OrganizationID: d.TenantID, ResourceID: d.RepositoryID}, d.Bindings)
}

func exactObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var f map[string]json.RawMessage
	if json.Unmarshal(raw, &f) != nil || len(f) != len(keys) {
		return nil, ErrInvalidSpecification
	}
	for _, k := range keys {
		v, ok := f[k]
		if !ok || bytes.Equal(v, []byte("null")) {
			return nil, ErrInvalidSpecification
		}
	}
	return f, nil
}

type Evaluation struct {
	SpecificationHash string          `json:"specification_hash,omitempty"`
	ContractHash      string          `json:"contract_hash,omitempty"`
	Status            evidence.Status `json:"status"`
	Code              string          `json:"code"`
	UnboundAssertions []string        `json:"unbound_assertions"`
	Checks            *policy.Result  `json:"checks,omitempty"`
}

// Evaluate is pure diagnostic execution of declarative rules. Trusted callers
// authenticate scope and evidence provenance; a hash is not authentication.
// Evaluating a previously generated artifact does not regenerate it or erase
// required checks on entitlement downgrade, matching policy evaluation semantics.
func (s Specification) Evaluate(ctx context.Context, in policy.Input) (Evaluation, error) {
	failure := func(err error) (Evaluation, error) {
		return Evaluation{Status: evidence.StatusError, Code: "INVALID_INPUT", UnboundAssertions: []string{}}, err
	}
	if err := contextError(ctx); err != nil {
		return failure(err)
	}
	if s.canonical == "" {
		return failure(ErrInvalidSpecification)
	}
	if s.repositoryID != in.RepositoryID {
		return failure(ErrInvalidInput)
	}
	checked, err := (policy.Engine{}).Evaluate(ctx, s.checks, in)
	if err != nil {
		return failure(err)
	}
	d, _ := s.checks.Document()
	hash, _ := s.contract.ContentHash()
	bound := map[string]bool{}
	sources := map[[2]string]bool{}
	for _, rule := range d.Rules {
		bound[rule.ID] = true
		req := rule.Require.Evidence[0]
		sources[[2]string{req.Producer, req.Type}] = true
	}
	for _, node := range in.Evidence {
		if err := contextError(ctx); err != nil {
			return failure(err)
		}
		f, _ := node.Fields() // Existing engine already validated all envelopes.
		if sources[[2]string{f.Producer, f.Type}] && f.InputHashes[ContractInputHash] != hash {
			return failure(ErrInvalidInput)
		}
	}
	r := Evaluation{SpecificationHash: s.ContentHash(), ContractHash: hash, Status: checked.Status, Code: string(checked.Code), UnboundAssertions: []string{}, Checks: &checked}
	assertions, _ := Assertions(s.contract)
	for _, a := range assertions {
		if !bound[a.ID] {
			r.UnboundAssertions = append(r.UnboundAssertions, a.ID)
		}
	}
	if len(r.UnboundAssertions) > 0 && r.Status != evidence.StatusFail {
		r.Status, r.Code = evidence.StatusUnknown, "UNBOUND_ASSERTIONS"
	}
	if err := contextError(ctx); err != nil {
		return failure(err)
	}
	return r, nil
}
