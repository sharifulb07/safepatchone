package githubapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"safepatch/internal/application"
	"safepatch/internal/authorization"
	"safepatch/internal/domain"
	"safepatch/internal/domain/feature"
	"safepatch/internal/entitlement"
	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/proof"
	"safepatch/internal/proof/passport"
	"safepatch/internal/sir"
	"safepatch/internal/tenant"
	"safepatch/internal/trust"
	"safepatch/internal/verification"
	"safepatch/languages/golang"
	"strings"
	"testing"
	"time"
)

type flowEntitlements struct{ enabled map[feature.Key]bool }

func (g flowEntitlements) Has(_ context.Context, _ string, k feature.Key) (bool, error) {
	return g.enabled[k], nil
}
func (g flowEntitlements) Limit(context.Context, string, feature.Key) (int64, error) {
	return 0, entitlement.ErrLimitUnspecified
}
func (g flowEntitlements) Resolve(context.Context, string, feature.Key) (entitlement.Resolution, error) {
	return entitlement.Resolution{}, nil
}

type tenantFunc func(context.Context, authorization.Principal, tenant.Scope) (tenant.Scope, error)

func (f tenantFunc) Resolve(c context.Context, a authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
	return f(c, a, s)
}

type changeSource struct {
	facts          application.ChangeFacts
	calls          int
	integrity      application.IntegrityInput
	integrityCalls int
	integrityError bool
	onIntegrity    func(int)
}

func (s *changeSource) LoadIntegrity(context.Context, domain.Change) (application.IntegrityInput, error) {
	s.integrityCalls++
	if s.onIntegrity != nil {
		s.onIntegrity(s.integrityCalls)
	}
	if s.integrityError {
		return application.IntegrityInput{}, errors.New("private integrity failure")
	}
	return s.integrity, nil
}

func (s *changeSource) Prepare(context.Context, domain.Change) (application.ChangeFacts, error) {
	s.calls++
	return s.facts, nil
}

type changeRunner struct {
	status verification.ExecutionStatus
	calls  int
}

func (r *changeRunner) Run(_ context.Context, request verification.ExecutionRequest) (verification.ExecutionResult, error) {
	r.calls++
	exit := 0
	if r.status != verification.Completed {
		exit = -1
	}
	var stdout []byte
	for _, arg := range request.Arguments {
		if arg == "test" {
			stdout = []byte("{\"Action\":\"start\",\"Package\":\"example.test/root\"}\n{\"Action\":\"run\",\"Package\":\"example.test/root\",\"Test\":\"TestFixture\"}\n{\"Action\":\"pass\",\"Package\":\"example.test/root\",\"Test\":\"TestFixture\"}\n{\"Action\":\"pass\",\"Package\":\"example.test/root\"}\n")
		}
	}
	return verification.ExecutionResult{Status: r.status, ExitCode: exit, Sandboxed: true, Stdout: stdout}, nil
}

type changeArchive struct {
	saved []application.ChangeArtifacts
	fail  bool
}

func (a *changeArchive) Persist(_ context.Context, _ domain.Change, v application.ChangeArtifacts) (string, error) {
	if a.fail {
		return "", errors.New("storage unavailable")
	}
	a.saved = append(a.saved, v)
	return "immutable_reference", nil
}

type changeAccess struct {
	allowed    bool
	quota      bool
	denyReview bool
}

func (a *changeAccess) Allowed(_ context.Context, _ authorization.Principal, _ string, key authorization.Key) (bool, error) {
	return a.allowed && !(a.denyReview && key == authorization.KeyPolicyManage), nil
}
func (a *changeAccess) Check(context.Context, application.Execution) error {
	if !a.quota {
		return errors.New("quota denied")
	}
	return nil
}

func workflowFixture(t *testing.T, overrides ...func(*application.ChangeWorkflowConfig)) (*application.ChangeWorkflow, domain.Change, *changeSource, *changeRunner, *changeArchive, *changeAccess) {
	t.Helper()
	change := domain.Change{OrganizationID: "org_a", RepositoryID: "repo", BaseSHA: strings.Repeat("a", 40), HeadSHA: strings.Repeat("b", 40)}
	subject := evidence.Subject{RepositoryID: "repo", Revision: change.HeadSHA}
	goVerifier, err := golang.New(golang.Config{GoTool: "go", Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := verification.NewRegistry(goVerifier)
	source := &changeSource{facts: application.ChangeFacts{Intent: intent.IntentContractFields{Goal: "Build the proposed change"}, Target: verification.Target{Subject: subject, Files: []string{"go.mod", "main.go"}, Metadata: map[string]string{"go.mod": "module example.test/root\ngo 1.26.0\n"}}, Extraction: sir.Extraction{Metadata: sir.Metadata{Schema: sir.Schema, TenantID: "org_a", Subject: subject, Producer: "fixture", ProducerVersion: "1.0.0", Coverage: sir.CoverageComplete, Deterministic: true}, Files: []sir.SourceFile{{Path: "main.go", ContentHash: evidence.Digest([]byte("package main"))}}}, Execution: verification.ExecutionRequest{Executable: "go", SourceDirectory: "/trusted/repo", Environment: goVerifier.Environment(), Limits: verification.ExecutionLimits{Timeout: time.Minute, StdoutBytes: 4096, StderrBytes: 4096, InputBytes: 4096, InputEntries: 100, WorkspaceBytes: 8192, TempBytes: 8192, CPUMillis: 1000, MemoryBytes: 1024 * 1024, PIDs: 32}}}}
	normalizer, _ := sir.NewNormalizer([]sir.Mapping{{Language: "go", NativeKind: "function", Kind: sir.Function}})
	obligations := proof.DefaultObligations()
	obligations[0].Capability = golang.Build
	planner, err := proof.NewProofPlanner("1.0.0", obligations, []proof.Provider{{ID: "go", Version: "1.0.0", Capability: golang.Build}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := policy.New(policy.Document{Schema: policy.Schema, PolicyID: "baseline", TenantID: "org_a", Version: "1.0.0", Rules: []policy.Rule{{ID: "required_build", Require: policy.Requirements{Evidence: []policy.EvidenceRequirement{{Producer: "go", Type: "go.build"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	access := &changeAccess{allowed: true, quota: true}
	policyRaw, _ := json.Marshal(p)
	for _, revision := range []string{change.BaseSHA, change.HeadSHA} {
		controls, err := verification.NewVerificationControls(verification.ControlFields{Schema: "safepatch.verification-controls.v1", OrganizationID: change.OrganizationID, Subject: evidence.Subject{RepositoryID: change.RepositoryID, Revision: revision}, Complete: true, Scanners: []verification.ControlCheck{{ID: string(golang.Build), Enabled: true, ConfigDigest: evidence.Digest([]byte("go-build-policy"))}}, Tests: []verification.ControlCheck{}, TimeoutMillis: 60000, SecurityExclusions: []string{}, Policy: policyRaw})
		if err != nil {
			t.Fatal(err)
		}
		if revision == change.BaseSHA {
			source.integrity.Base = controls
		} else {
			source.integrity.Head = controls
		}
	}
	grants := flowEntitlements{enabled: map[feature.Key]bool{feature.KeyVerifyBasic: true}}
	tenants := tenantFunc(func(_ context.Context, _ authorization.Principal, s tenant.Scope) (tenant.Scope, error) {
		return s, nil
	})
	req := application.Requirement{Feature: feature.KeyVerifyBasic, Permission: authorization.KeyProofExecute}
	guard, _ := application.NewExecutionGuard(tenants, grants, access, access, req)
	runner := &changeRunner{status: verification.Completed}
	archive := &changeArchive{}
	config := application.ChangeWorkflowConfig{Guard: guard, Source: source, Normalizer: normalizer, Planner: planner, Registry: registry, Runner: runner, Artifacts: archive, Policy: p, Target: proof.L0, Providers: []proof.ProviderState{{ID: "go", Availability: proof.Available}}, Requirements: map[verification.Capability]application.Requirement{golang.Build: req}, StepGuard: func(ctx context.Context, a authorization.Principal, s tenant.Scope, r application.Requirement) error {
		g, e := application.NewExecutionGuard(tenants, grants, access, access, r)
		if e != nil {
			return e
		}
		_, e = g.Authorize(ctx, a, s)
		return e
	}, Now: func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }, Deterministic: true, Confidence: 1}
	for _, override := range overrides {
		override(&config)
	}
	config.Integrity = source
	core, err := application.NewChangeWorkflow(config)
	if err != nil {
		t.Fatal(err)
	}
	return core, change, source, runner, archive, access
}
func TestLocalAndGitHubUseIdenticalGoCoreArtifacts(t *testing.T) {
	core, change, _, runner, archive, _ := workflowFixture(t)
	actor := authorization.Principal{ID: "user", Kind: authorization.User}
	local, err := core.Run(t.Context(), actor, change, "run")
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := core.RunHosted(t.Context(), actor, change, "run")
	if err != nil {
		t.Fatal(err)
	}
	if local != hosted || local.Decision != trust.Pass || runner.calls != 2 || len(archive.saved) != 2 || !reflect.DeepEqual(archive.saved[0], archive.saved[1]) {
		t.Fatal("core divergence", local, hosted, runner.calls)
	}
	v := archive.saved[0]
	evidenceFields, evidenceErr := v.Evidence[0].Fields()
	if evidenceErr != nil || evidenceFields.InputHashes["verification_integrity_base"] != v.Integrity.BaseDigest || evidenceFields.InputHashes["verification_integrity_head"] != v.Integrity.HeadDigest {
		t.Fatal("integrity not bound into evidence", evidenceErr)
	}
	if hash, e := v.Intent.ContentHash(); e != nil || hash == "" {
		t.Fatal("intent missing", e)
	}
	if len(v.Graph) != 1 || len(v.Evidence) != 1 || v.Plan.Hash() == "" || v.SIR.ContentHash() == "" || v.Policy.Status != evidence.StatusPass {
		t.Fatal("stage missing", v)
	}
	pf, err := v.Passport.Fields()
	intentDigest, _ := v.Intent.ContentHash()
	policyJSON, _ := json.Marshal(v.Policy)
	if err != nil || pf.Change != change || pf.ChangeID != change.ID() || pf.Commit != change.HeadSHA || pf.IntentDigest != intentDigest || pf.ProofPlanDigest != v.Plan.Hash() || pf.PolicyDecision.ResultDigest != evidence.Digest(policyJSON) || pf.EvidenceGraphDigest != v.EvidenceGraph.Digest() || pf.ProofLevel != proof.L0 || pf.VerificationDecision != trust.Pass {
		t.Fatal("Passport is not bound to actual run artifacts", pf, err)
	}
	if pf.ArtifactDigest != nil || pf.SBOM != nil || pf.Provenance != nil {
		t.Fatal("fabricated build attestations")
	}
	altered := change
	altered.BaseSHA = strings.Repeat("c", 40)
	if altered.ID() == change.ID() {
		t.Fatal("base omitted from ChangeID")
	}
	altered = change
	altered.OrganizationID = "other"
	if altered.ID() == change.ID() {
		t.Fatal("tenant omitted from ChangeID")
	}
}

func TestProofPlanReusesOneGoVerifierForBuildAndTest(t *testing.T) {
	core, change, _, runner, archive, _ := workflowFixture(t, func(c *application.ChangeWorkflowConfig) {
		obligations := proof.DefaultObligations()
		obligations[0].Capability = golang.Build
		obligations[1].Capability = golang.Build
		obligations[2].Capability = golang.Test
		p, err := proof.NewProofPlanner("1.0.0", obligations, []proof.Provider{{ID: "go-build", Version: "1.0.0", Capability: golang.Build}, {ID: "go-test", Version: "1.0.0", Capability: golang.Test}})
		if err != nil {
			t.Fatal(err)
		}
		c.Planner = p
		c.Target = proof.L2
		c.Providers = []proof.ProviderState{{ID: "go-build", Availability: proof.Available}, {ID: "go-test", Availability: proof.Available}}
		c.VerifierIDs = map[string]string{"go-build": "go", "go-test": "go"}
		c.Requirements[golang.Test] = c.Requirements[golang.Build]
	})
	result, err := core.RunHosted(t.Context(), authorization.Principal{ID: "user", Kind: authorization.User}, change, "build_and_test")
	if err != nil || result.Decision != trust.Pass || runner.calls != 2 || len(archive.saved) != 1 || len(archive.saved[0].Evidence) != 2 {
		t.Fatal(result, err, runner.calls, len(archive.saved))
	}
}
func TestChangeWorkflowFailClosedAndRetainsHistory(t *testing.T) {
	for _, mode := range []string{"unknown_execution", "partial_sir", "foreign_sir", "storage_error", "permission_revoked", "quota_denied", "unsafe_hosted"} {
		t.Run(mode, func(t *testing.T) {
			core, change, source, runner, archive, access := workflowFixture(t)
			actor := authorization.Principal{ID: "user", Kind: authorization.User}
			if _, err := core.RunHosted(t.Context(), actor, change, "prior"); err != nil {
				t.Fatal(err)
			}
			prior := archive.saved[0]
			switch mode {
			case "unknown_execution":
				runner.status = verification.Unknown
			case "partial_sir":
				source.facts.Extraction.Coverage = sir.CoveragePartial
			case "foreign_sir":
				source.facts.Extraction.TenantID = "other"
			case "storage_error":
				archive.fail = true
			case "permission_revoked":
				access.allowed = false
			case "quota_denied":
				access.quota = false
			case "unsafe_hosted":
				source.facts.Execution.Mode = verification.TrustedLocal
			}
			result, _ := core.RunHosted(t.Context(), actor, change, "next")
			if result.Decision != trust.Unknown {
				t.Fatal("unknown/revocation became PASS", result)
			}
			if mode == "unknown_execution" || mode == "partial_sir" {
				if len(archive.saved) != 2 {
					t.Fatal("UNKNOWN Passport missing")
				}
				p, err := archive.saved[1].Passport.Fields()
				if err != nil || p.ProofLevel != passport.UnknownLevel || p.VerificationDecision != trust.Unknown {
					t.Fatal("Passport promoted incomplete proof", p, err)
				}
			}
			if !reflect.DeepEqual(prior, archive.saved[0]) {
				t.Fatal("existing evidence corrupted")
			}
			if (mode == "permission_revoked" || mode == "quota_denied") && source.calls != 1 {
				t.Fatal("source reached after denied admission")
			}
		})
	}
}
