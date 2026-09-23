package evidencestore

import (
	"bytes"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
	"safepatch/internal/evidence"
)

func TestSealedEvidencePersistenceAndTenantBoundary(t *testing.T) {
	scope := scope()
	store, err := NewLocal(t.TempDir(), scope)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	original, err := store.Put(t.Context(), scope, []byte("original scanner output"))
	if err != nil {
		t.Fatal(err)
	}
	fields := evidence.EvidenceFields{EvidenceID: "evidence_1", TenantID: scope.OrganizationID, ChangeID: "change_1", RunID: "run_1", Producer: "fixture", ProducerVersion: "1.0.0", Category: "verification", Type: "fixture.check", Status: evidence.StatusUnknown, Severity: evidence.SeverityInfo, Summary: "Incomplete scanner evidence.", Artifacts: []evidence.Artifact{{ArtifactID: "stdout", ContentHash: original.SHA256, SizeBytes: original.Bytes}}, CreatedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)}
	sealed, err := evidence.NewEvidence(fields)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := sealed.CanonicalJSON()
	ref, err := store.Put(t.Context(), scope, want)
	if err != nil {
		t.Fatal(err)
	}
	// Changes to external commercial metadata never modify this stored schema.
	for _, offering := range edition.Editions() {
		t.Run(offering.Name, func(t *testing.T) {
			got, err := store.Get(t.Context(), scope, ref)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("evidence changed", err)
			}
			decoded, err := evidence.DecodeEvidence(got)
			if err != nil {
				t.Fatal(err)
			}
			f, _ := decoded.Fields()
			if f.TenantID != scope.OrganizationID || f.Status != evidence.StatusUnknown || f.Artifacts[0].ContentHash != original.SHA256 {
				t.Fatal("scope/status/reference lost")
			}
		})
	}
	other := scope
	other.OrganizationID = "tenant_other"
	if _, err := store.Get(t.Context(), other, ref); err == nil {
		t.Fatal("cross-tenant read")
	}
	// New evidence has a new identity. Neither object nor original bytes change.
	fields.EvidenceID = "evidence_2"
	fields.RunID = "run_2"
	fields.Status = evidence.StatusPass
	next, err := evidence.NewEvidence(fields)
	if err != nil {
		t.Fatal(err)
	}
	nextRaw, _ := next.CanonicalJSON()
	if _, err := store.Put(t.Context(), scope, nextRaw); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(t.Context(), scope, ref)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("prior evidence overwritten", err)
	}
	got, err = store.Get(t.Context(), scope, original)
	if err != nil || string(got) != "original scanner output" {
		t.Fatal("original altered", err)
	}
}
