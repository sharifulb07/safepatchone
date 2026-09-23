package evidencestore

import (
	"bytes"
	"testing"
	"time"

	"safepatch/internal/evidence"
	"safepatch/internal/intent"
	"safepatch/internal/policy"
	"safepatch/internal/spec"
	"safepatch/internal/tenant"
)

func TestStoredSpecificationRoundTripExecutionAndIsolation(t *testing.T) {
	s := scope()
	store, err := NewLocal(t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, _ := intent.NewIntentContract(intent.IntentContractFields{Goal: "Preserve receipts", AcceptanceCriteria: []string{"Return original receipt"}})
	a, _ := spec.Assertions(c)
	generated, err := (spec.SpecForge{}).Generate(t.Context(), c, tenant.Scope{OrganizationID: s.OrganizationID, ResourceID: s.Subject.RepositoryID}, []spec.Binding{{AssertionID: a[0].ID, Evidence: policy.EvidenceRequirement{Producer: "receipt-test", Type: "receipt.retry"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := generated.MarshalJSON()
	ref, err := store.Put(t.Context(), s, raw)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.Get(t.Context(), s, ref)
	if err != nil || !bytes.Equal(stored, raw) {
		t.Fatal(err)
	}
	decoded, err := spec.DecodeSpecification(t.Context(), stored)
	if err != nil || decoded.ContentHash() != generated.ContentHash() {
		t.Fatal(err)
	}
	hash, _ := c.ContentHash()
	now := time.Now().UTC()
	node, err := evidence.NewEvidence(evidence.EvidenceFields{EvidenceID: "receipt", TenantID: s.OrganizationID, ChangeID: "change", RunID: "run", Producer: "receipt-test", ProducerVersion: "1.0.0", Category: "verification", Type: "receipt.retry", Deterministic: true, Status: evidence.StatusPass, Severity: evidence.SeverityInfo, Confidence: 1, Summary: "Receipt test passed", InputHashes: map[string]string{spec.ContractInputHash: hash}, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := decoded.Evaluate(t.Context(), policy.Input{TenantID: s.OrganizationID, RepositoryID: s.Subject.RepositoryID, ChangeID: "change", RunID: "run", AsOf: now, Criticality: policy.CriticalityUnknown, Evidence: []evidence.Evidence{node}})
	if err != nil || result.Status != evidence.StatusPass {
		t.Fatal(result, err)
	}
	for _, kind := range []string{"tenant", "repository", "revision"} {
		foreign := s
		switch kind {
		case "tenant":
			foreign.OrganizationID = "other"
		case "repository":
			foreign.Subject.RepositoryID = "other"
		case "revision":
			foreign.Subject.Revision = "other"
		}
		if _, err := store.Get(t.Context(), foreign, ref); err == nil {
			t.Fatal("cross-scope read", kind)
		}
		if _, err := store.Put(t.Context(), foreign, raw); err == nil {
			t.Fatal("cross-scope write", kind)
		}
	}
	f, _ := c.Fields()
	f.Requirements = []string{"Retain history"}
	changed, _ := intent.NewIntentContract(f)
	updated, err := (spec.SpecForge{}).Generate(t.Context(), changed, tenant.Scope{OrganizationID: s.OrganizationID, ResourceID: s.Subject.RepositoryID}, []spec.Binding{{AssertionID: a[0].ID, Evidence: policy.EvidenceRequirement{Producer: "receipt-test", Type: "receipt.retry"}}})
	if err != nil {
		t.Fatal(err)
	}
	newRaw, _ := updated.MarshalJSON()
	newRef, err := store.Put(t.Context(), s, newRaw)
	if err != nil || newRef == ref {
		t.Fatal("version overwritten", err)
	}
	old, err := store.Get(t.Context(), s, ref)
	if err != nil || !bytes.Equal(old, raw) {
		t.Fatal("previous artifact changed", err)
	}
}
