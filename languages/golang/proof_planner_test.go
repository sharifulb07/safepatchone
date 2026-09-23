package golang

import (
	"testing"

	"safepatch/internal/proof"
	"safepatch/internal/verification"
)

// Component integration: the shared planner selects existing Go SDK operations;
// the existing verifier expands them into Runner requests without new Go logic.
// Availability here is a trusted test fixture, not a tool installation check.
func TestProofPlanExpandsThroughExistingGoVerifier(t *testing.T) {
	verifier, request := fixture(t)
	registry, err := verification.NewRegistry(verifier)
	if err != nil {
		t.Fatal(err)
	}
	obligations := proof.DefaultObligations()
	obligations[0].Capability, obligations[1].Capability = Build, Build
	obligations[2].Capability, obligations[3].Capability = Test, Vulncheck
	providers := []proof.Provider{}
	states := []proof.ProviderState{}
	for _, capability := range []verification.Capability{Build, Test, Vulncheck} {
		descriptors := registry.Discover(capability)
		if len(descriptors) != 1 {
			t.Fatal("existing verifier missing", capability)
		}
		providers = append(providers, proof.Provider{ID: string(capability), Version: descriptors[0].Version, Capability: capability})
		states = append(states, proof.ProviderState{ID: string(capability), Availability: proof.Available})
	}
	planner, err := proof.NewProofPlanner("1", obligations, providers)
	if err != nil {
		t.Fatal(err)
	}
	input := proof.Request{TenantID: "org", Subject: request.Target.Subject, Target: proof.L3, Providers: states}
	plan, err := planner.Plan(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	fields, _ := plan.Fields()
	if fields.State != proof.Ready || len(fields.Steps) != 3 || len(fields.Resolutions) != 4 {
		t.Fatal(fields)
	}
	for _, step := range fields.Steps {
		request.Capability = step.Provider.Capability
		execution, err := verifier.Plan(t.Context(), request)
		if err != nil || len(execution.Steps) == 0 {
			t.Fatal(err)
		}
		for _, operation := range execution.Steps {
			if operation.Request.Limits != request.BaseExecution.Limits || operation.Request.SourceDirectory != request.BaseExecution.SourceDirectory {
				t.Fatal("Runner policy replaced")
			}
		}
	}
	input.Target = proof.L8
	unsupported, err := planner.Plan(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	blocked, _ := unsupported.Fields()
	if blocked.State != proof.Blocked || blocked.Resolutions[8].Code != proof.ProviderUnavailable {
		t.Fatal("formal provider fabricated")
	}
}
