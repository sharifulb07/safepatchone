package application

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"safepatch/internal/audit"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/evidence"
	"safepatch/internal/graph"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/sir"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"safepatch/internal/verification"
	"time"
)

var ErrChangeWorkflow = errors.New("change workflow unavailable or invalid")

// ChangeSource is a trusted bounded source/extractor adapter. It must pin both
// revisions, never execute repository code, and report incomplete extraction.
// It supplies facts only; policy, providers, budgets and runner are server-owned.
type ChangeSource interface {
	Prepare(context.Context, domain.Change) (ChangeFacts, error)
}
type ChangeFacts struct {
	Intent     intent.IntentContractFields
	Extraction sir.Extraction
	Target     verification.Target
	Execution  verification.ExecutionRequest
	// Optional immutable build-output references resolved by the trusted source.
	// Nil is unknown; no SBOM or provenance is fabricated from scanner prose.
	ArtifactDigest *string
	SBOM           *passport.Reference
	Provenance     *passport.Reference
}
type ChangeArtifacts struct {
	Authorization   trust.KernelResult           `json:"authorization"`
	ControlReviewID string                       `json:"control_review_id,omitempty"`
	Integrity       verification.IntegrityResult `json:"verification_integrity"`
	ChangeID        string                       `json:"change_id"`
	RunID           string                       `json:"run_id"`
	Intent          intent.IntentContract        `json:"intent"`
	SIR             sir.Representation           `json:"sir"`
	Graph           []graph.ChangeNode           `json:"change_graph"`
	Plan            proof.Plan                   `json:"proof_plan"`
	Evidence        []evidence.Evidence          `json:"evidence"`
	Policy          policy.Result                `json:"policy"`
	Decision        trust.Decision               `json:"decision"`
	EvidenceGraph   graph.EvidenceManifest       `json:"evidence_graph"`
	Passport        passport.Passport            `json:"passport"`
}

// Persist must commit immutable evidence/artifacts under this tenant and return
// a durable reference before any success Check can be emitted.
type ChangeArtifactStore interface {
	Persist(context.Context, domain.Change, ChangeArtifacts) (string, error)
}
type ChangeResult struct {
	Authorization     trust.AuthorizationDecision
	ChangeID          string
	Decision          trust.Decision
	EvidenceReference string
}
type ChangeWorkflowConfig struct {
	// Optional trusted technical assessments. Absence cannot yield AUTHORIZE.
	FinalAuthorization *FinalAuthorizationConfig
	// Optional only for existing SaaS/local composition. Customer-managed hosts
	// install DeploymentAccess; both modes still execute this same workflow.
	Deployment *DeploymentAccess
	// Shared baseline capture, independent of enterprise.audit export ownership.
	Audit audit.Store
	// Mandatory for every edition and both local/hosted entrypoints.
	Integrity    VerificationIntegritySource
	Guard        *ExecutionGuard
	Source       ChangeSource
	Normalizer   *sir.Normalizer
	Planner      *proof.ProofPlanner
	Registry     *verification.Registry
	Runner       verification.Runner
	Artifacts    ChangeArtifactStore
	Policy       policy.Policy
	Target       proof.Level
	Providers    []proof.ProviderState
	Budgets      []proof.Budget
	Requirements map[verification.Capability]Requirement
	// Planner providers may select several capabilities from one registered SDK.
	VerifierIDs map[string]string
	// StepGuard uses the same resolver/permissions and atomic quota implementation
	// as Guard. It must admit each configured verifier capability before execution.
	StepGuard     func(context.Context, authorization.Principal, tenant.Scope, Requirement) error
	Now           func() time.Time
	Deterministic bool
	Confidence    float64
}
type ChangeWorkflow struct{ c ChangeWorkflowConfig }

func (w *ChangeWorkflow) PolicyHash() string {
	if w == nil {
		return ""
	}
	return w.c.Policy.Hash()
}

func NewChangeWorkflow(c ChangeWorkflowConfig) (*ChangeWorkflow, error) {
	if c.FinalAuthorization != nil {
		if c.FinalAuthorization.Source == nil || c.FinalAuthorization.Controls.Validate() != nil {
			return nil, ErrChangeWorkflow
		}
		copy := *c.FinalAuthorization
		c.FinalAuthorization = &copy
	}
	if c.Integrity == nil || c.Guard == nil || c.Source == nil || c.Normalizer == nil || c.Planner == nil || c.Registry == nil || c.Runner == nil || c.Artifacts == nil || c.StepGuard == nil || c.Now == nil || c.Target.Rank() < 0 || c.Policy.Hash() == "" || len(c.Requirements) == 0 || math.IsNaN(c.Confidence) || c.Confidence < 0 || c.Confidence > 1 {
		return nil, ErrChangeWorkflow
	}
	c.Providers = append([]proof.ProviderState{}, c.Providers...)
	c.Budgets = append([]proof.Budget{}, c.Budgets...)
	requirements := map[verification.Capability]Requirement{}
	for key, r := range c.Requirements {
		if !key.Valid() || feature.ValidateKey(r.Feature) != nil || authorization.ValidateKey(r.Permission) != nil {
			return nil, ErrChangeWorkflow
		}
		requirements[key] = r
	}
	c.Requirements = requirements
	verifiers := map[string]string{}
	if len(c.VerifierIDs) > proof.MaxProviders {
		return nil, ErrChangeWorkflow
	}
	for provider, id := range c.VerifierIDs {
		if !evidence.ValidIdentifier(provider) || !evidence.ValidIdentifier(id) {
			return nil, ErrChangeWorkflow
		}
		verifiers[provider] = id
	}
	c.VerifierIDs = verifiers
	return &ChangeWorkflow{c}, nil
}

// Run is the one shared core for admitted local and GitHub work. It contains no
// provider, plan-name, checkout, HTTP or production-deployment logic.
func (w *ChangeWorkflow) Run(ctx context.Context, actor authorization.Principal, change domain.Change, run string) (result ChangeResult, err error) {
	return w.run(ctx, actor, change, run, false)
}

// RunHosted changes only the execution isolation requirement, never verification
// semantics or edition behavior. Local callers may use the same sandbox too.
func (w *ChangeWorkflow) RunHosted(ctx context.Context, actor authorization.Principal, change domain.Change, run string) (ChangeResult, error) {
	return w.run(ctx, actor, change, run, true)
}
func (w *ChangeWorkflow) run(ctx context.Context, actor authorization.Principal, change domain.Change, run string, hosted bool) (result ChangeResult, err error) {
	result.Decision = trust.Unknown
	result.Authorization = trust.Block
	defer func() {
		if err != nil {
			result.Authorization = trust.Block
		}
	}()
	if w == nil || ctx == nil || change.Validate() != nil || !evidence.ValidIdentifier(run) {
		return result, ErrChangeWorkflow
	}
	scope := tenant.Scope{OrganizationID: change.OrganizationID, ResourceID: change.RepositoryID}
	if w.c.Audit != nil {
		defer func() {
			outcome := "SUCCEEDED"
			if err != nil {
				outcome = "FAILED"
			} else if result.Decision == trust.Unknown {
				outcome = "UNKNOWN"
			}
			event := audit.Event{ID: "proof_" + evidence.Digest([]byte(change.RepositoryID+"\n"+run)), OrganizationID: change.OrganizationID, Actor: actor, Action: "proof.verify", ResourceID: change.RepositoryID, Outcome: outcome, OccurredAt: w.c.Now().UTC().Truncate(time.Microsecond)}
			if auditErr := w.c.Audit.Append(ctx, change.OrganizationID, event); auditErr != nil {
				result.Decision = trust.Unknown
				err = audit.ErrUnavailable
			}
		}()
	}
	if w.c.Deployment != nil {
		if err = w.c.Deployment.Authorize(ctx, actor, scope); err != nil {
			return result, err
		}
	}
	if _, err = w.c.Guard.Authorize(ctx, actor, scope); err != nil {
		return result, err
	}
	result.ChangeID = change.ID()
	integrity, controlInput, err := w.checkIntegrity(ctx, actor, change)
	if err != nil {
		return result, err
	}
	document, err := w.c.Policy.Document()
	if err != nil || document.TenantID != change.OrganizationID {
		return result, ErrChangeWorkflow
	}
	facts, err := w.c.Source.Prepare(ctx, change)
	if err != nil {
		return result, err
	}
	baselineControls, _ := controlInput.Base.Fields()
	if facts.Execution.Limits.Timeout < time.Duration(baselineControls.TimeoutMillis)*time.Millisecond {
		return result, verification.ErrIntegrity
	}
	if hosted && facts.Execution.Mode == verification.TrustedLocal {
		return result, verification.ErrSandboxRequired
	}
	subject := evidence.Subject{RepositoryID: change.RepositoryID, Revision: change.HeadSHA}
	if facts.Target.Subject != subject || facts.Extraction.Subject != subject || facts.Extraction.TenantID != change.OrganizationID || facts.Target.Validate() != nil {
		return result, ErrChangeWorkflow
	}
	contract, err := intent.NewIntentContract(facts.Intent)
	if err != nil {
		return result, err
	}
	representation, err := w.c.Normalizer.Normalize(ctx, facts.Extraction)
	if err != nil {
		return result, err
	}
	fields, err := representation.Fields()
	if err != nil {
		return result, err
	}
	now := w.c.Now().UTC().Truncate(time.Microsecond)
	nodes := []graph.ChangeNode{}
	for _, f := range fields.Files {
		payload, _ := json.Marshal(map[string]string{"sir_hash": representation.ContentHash()})
		node, e := graph.NewChangeNode(graph.ChangeNodeFields{Schema: graph.ChangeNodeSchema, TenantID: change.OrganizationID, Ref: graph.EntityRef{RepositoryID: change.RepositoryID, Revision: change.HeadSHA, EntityID: "file_" + evidence.Digest([]byte(f.Path))}, Kind: graph.FileEntity, Name: f.Path, Path: f.Path, InputHash: f.ContentHash, Payload: payload, CreatedAt: now})
		if e != nil {
			return result, e
		}
		nodes = append(nodes, node)
	}
	for _, fact := range fields.Nodes {
		kind := graph.SymbolEntity
		switch fact.Kind {
		case sir.API:
			kind = graph.APIEntity
		case sir.DBOperation:
			kind = graph.DBEntity
		case sir.Dependency:
			kind = graph.DependencyEntity
		case sir.Test:
			kind = graph.TestEntity
		}
		payload, e := json.Marshal(fact)
		if e != nil {
			return result, e
		}
		node, e := graph.NewChangeNode(graph.ChangeNodeFields{Schema: graph.ChangeNodeSchema, TenantID: change.OrganizationID, Ref: graph.EntityRef{RepositoryID: change.RepositoryID, Revision: change.HeadSHA, EntityID: "sir_" + evidence.Digest([]byte(fact.ID))}, Kind: kind, Name: fact.Name, Path: fact.Source.Path, InputHash: representation.ContentHash(), Payload: payload, CreatedAt: now})
		if e != nil {
			return result, e
		}
		nodes = append(nodes, node)
	}
	plan, err := w.c.Planner.Plan(ctx, proof.Request{TenantID: change.OrganizationID, Subject: subject, Target: w.c.Target, Providers: w.c.Providers, Budgets: w.c.Budgets})
	if err != nil {
		return result, err
	}
	pf, err := plan.Fields()
	if err != nil {
		return result, err
	}
	artifacts := ChangeArtifacts{Integrity: integrity, ChangeID: result.ChangeID, RunID: run, Intent: contract, SIR: representation, Graph: nodes, Plan: plan, Evidence: []evidence.Evidence{}, Decision: trust.Unknown}
	if integrity.Changed {
		artifacts.ControlReviewID = controlInput.Review.ID
	}
	statuses := []evidence.Status{}
	coverage := trust.Coverage{Required: []string{}, Observed: map[string][]string{}}
	totalEvidenceBytes := 0
	complete := pf.State == proof.Ready && len(pf.Steps) > 0 && fields.Coverage == sir.CoverageComplete
	if pf.State == proof.Ready {
		planned := map[string]bool{}
		for _, step := range pf.Steps {
			planned[string(step.Provider.Capability)] = true
			coverage.Required = append(coverage.Required, step.ID)
		}
		for _, check := range append(append([]verification.ControlCheck{}, baselineControls.Scanners...), baselineControls.Tests...) {
			if check.Enabled && !planned[check.ID] {
				return result, verification.ErrIntegrity
			}
		}
		for _, step := range pf.Steps {
			req, ok := w.c.Requirements[step.Provider.Capability]
			if !ok {
				return result, ErrChangeWorkflow
			}
			if err = w.c.StepGuard(ctx, actor, scope, req); err != nil {
				return result, err
			}
			current, currentInput, checkErr := w.checkIntegrity(ctx, actor, change)
			if checkErr != nil || current.BaseDigest != integrity.BaseDigest || current.HeadDigest != integrity.HeadDigest || integrity.Changed && currentInput.Review.Digest() != controlInput.Review.Digest() {
				return result, verification.ErrIntegrity
			}
			verifierID := w.c.VerifierIDs[step.Provider.ID]
			if verifierID == "" {
				verifierID = step.Provider.ID
			}
			executionRunner := w.c.Runner
			if w.c.Deployment != nil {
				executionRunner = deploymentRunner{w.c.Runner, w.c.Deployment, actor, scope}
			}
			record, e := w.c.Registry.Run(ctx, verifierID, verification.PlanRequest{Target: facts.Target, Capability: step.Provider.Capability, BaseExecution: facts.Execution}, integrityRunner{executionRunner, time.Duration(baselineControls.TimeoutMillis) * time.Millisecond})
			if e != nil {
				complete = false
				statuses = append(statuses, evidence.StatusUnknown)
				continue
			}
			if record.VerifierVersion != step.Provider.Version {
				return result, ErrChangeWorkflow
			}
			if hosted {
				for _, execution := range record.Executions {
					if !execution.Sandboxed {
						complete = false
					}
				}
			}
			intentHash, hashErr := contract.ContentHash()
			if hashErr != nil {
				return result, hashErr
			}
			inputHashes := map[string]string{"intent": intentHash, "sir": representation.ContentHash(), "verification_integrity_base": integrity.BaseDigest, "verification_integrity_head": integrity.HeadDigest}
			if integrity.Changed {
				inputHashes["verification_control_review"] = controlInput.Review.Digest()
			}
			node, e := evidence.FromVerification(record, evidence.VerificationIdentity{EvidenceID: "ev_" + evidence.Digest([]byte(run+":"+step.ID)), TenantID: change.OrganizationID, ChangeID: result.ChangeID, RunID: run, CreatedAt: now, Deterministic: w.c.Deterministic, Confidence: w.c.Confidence, InputHashes: inputHashes})
			if e != nil {
				return result, e
			}
			encoded, e := node.CanonicalJSON()
			if e != nil {
				return result, e
			}
			totalEvidenceBytes += len(encoded)
			if totalEvidenceBytes > policy.MaxInputBytes {
				return result, ErrChangeWorkflow
			}
			artifacts.Evidence = append(artifacts.Evidence, node)
			observation, _ := node.Fields()
			coverage.Observed[step.ID] = []string{observation.EvidenceID}
			statuses = append(statuses, record.Report.Status)
		}
	}
	// Proof level is derived only when every required planned observation passes.
	var achieved *uint32
	if trust.Decide(complete, statuses...) == trust.Pass {
		level := uint32(pf.Target.Rank())
		achieved = &level
	}
	level := passport.UnknownLevel
	if achieved != nil {
		level = pf.Target
	}
	artifacts.Authorization, err = w.finalAuthorization(ctx, change, artifacts, level, complete, coverage)
	if err != nil {
		return result, err
	}
	artifacts.Policy = artifacts.Authorization.Policy
	statuses = append(statuses, artifacts.Policy.Status)
	artifacts.Decision = trust.Decide(complete, statuses...)
	artifacts.EvidenceGraph, err = graph.NewEvidenceManifest(change, run, artifacts.Evidence, nil)
	if err != nil {
		return result, err
	}
	intentDigest, err := artifacts.Intent.ContentHash()
	if err != nil {
		return result, err
	}
	policyJSON, err := json.Marshal(artifacts.Policy)
	if err != nil {
		return result, err
	}
	artifacts.Passport, err = passport.New(passport.Fields{
		Change: change, ChangeID: change.ID(), RunID: run, Commit: change.HeadSHA,
		IntentDigest: intentDigest, ArtifactDigest: facts.ArtifactDigest, SBOM: facts.SBOM, Provenance: facts.Provenance,
		ProofLevel: level, ProofPlanDigest: plan.Hash(),
		PolicyDecision:      passport.PolicyDecision{PolicyDigest: artifacts.Policy.PolicyHash, ResultDigest: evidence.Digest(policyJSON), Status: artifacts.Policy.Status},
		EvidenceGraphDigest: artifacts.EvidenceGraph.Digest(), VerificationDecision: artifacts.Decision, IssuedAt: now,
	})
	if err != nil {
		return result, err
	}
	current, currentInput, checkErr := w.checkIntegrity(ctx, actor, change)
	if checkErr != nil || current.BaseDigest != integrity.BaseDigest || current.HeadDigest != integrity.HeadDigest || integrity.Changed && currentInput.Review.Digest() != controlInput.Review.Digest() {
		return result, verification.ErrIntegrity
	}
	reference, err := w.c.Artifacts.Persist(ctx, change, artifacts)
	if err != nil || reference == "" || len(reference) > 256 {
		return result, ErrChangeWorkflow
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Decision = artifacts.Decision
	result.Authorization = artifacts.Authorization.Decision
	result.EvidenceReference = reference
	return result, nil
}
