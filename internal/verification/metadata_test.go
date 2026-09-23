package verification

import (
	"context"
	"strings"
	"testing"
)

func TestManifestMetadataIsBoundedAndCopiedBetweenStages(t *testing.T) {
	v, input, runner := sdkFixture(t)
	input.Target.Metadata = map[string]string{"go.mod": "original"}
	v.detect = func(_ context.Context, target Target) (Detection, error) {
		target.Metadata["go.mod"] = "forged"
		return Detection{State: Detected, Capabilities: []Capability{"test"}}, nil
	}
	v.plan = func(_ context.Context, request PlanRequest) (Plan, error) {
		if request.Target.Metadata["go.mod"] != "original" {
			t.Fatal("detection mutated planning metadata")
		}
		request.Target.Metadata["go.mod"] = "changed"
		return Plan{Steps: []Step{{ID: "run", Request: request.BaseExecution}}}, nil
	}
	registry, _ := NewRegistry(v)
	if _, err := registry.Run(t.Context(), v.id, input, runner); err != nil {
		t.Fatal(err)
	}
	if input.Target.Metadata["go.mod"] != "original" {
		t.Fatal("metadata aliases caller")
	}
	input.Target.Metadata["../other"] = "unlisted"
	if input.Target.Validate() == nil {
		t.Fatal("unlisted metadata accepted")
	}
	delete(input.Target.Metadata, "../other")
	input.Target.Metadata["go.mod"] = strings.Repeat("x", 1024*1024+1)
	if input.Target.Validate() == nil {
		t.Fatal("unbounded manifest accepted")
	}
}
