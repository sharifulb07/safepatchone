package evidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"safepatch/internal/domain/edition"
)

func fieldsFixture() EvidenceFields {
	return EvidenceFields{EvidenceID: "ev_1", TenantID: "tenant_1", ChangeID: "change_1", RunID: "run_1", Producer: "scanner.fixture", ProducerVersion: "1.2.3", Category: "verification", Type: "scanner.fixture", Deterministic: true, Status: StatusPass, Severity: SeverityInfo, Confidence: 0.75, Summary: "Fixture completed.", Payload: json.RawMessage(`{"z":2,"nested":{"b":2,"a":1},"a":9007199254740993}`), SourceLocations: []SourceLocation{{Path: "src/main.go", StartLine: 2, StartColumn: 1, EndLine: 2, EndColumn: 8}}, Artifacts: []Artifact{{ArtifactID: "artifact_1", ContentHash: Digest([]byte("original")), SizeBytes: 8}}, InputHashes: map[string]string{"source": Digest([]byte("input"))}, CreatedAt: time.Date(2026, 9, 6, 12, 30, 0, 123, time.FixedZone("offset", 21600))}
}

func TestEvidenceRoundTripAndExactSchema(t *testing.T) {
	e, err := NewEvidence(fieldsFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 19 {
		t.Fatal("wrong schema")
	}
	for _, key := range []string{"evidence_id", "tenant_id", "change_id", "run_id", "producer", "producer_version", "category", "type", "deterministic", "status", "severity", "confidence", "summary", "payload", "source_locations", "artifacts", "input_hashes", "content_hash", "created_at"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing", key)
		}
	}
	decoded, err := DecodeEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := decoded.CanonicalJSON()
	if !bytes.Equal(raw, again) {
		t.Fatal("round trip changed bytes")
	}
	f, _ := e.Fields()
	if f.CreatedAt.Location() != time.UTC || !f.CreatedAt.Equal(fieldsFixture().CreatedAt) || !validSHA256(f.ContentHash) {
		t.Fatal(f)
	}
	if !bytes.Contains(f.Payload, []byte("9007199254740993")) {
		t.Fatal("number lost precision")
	}
	f.ContentHash = ""
	unsigned, _ := json.Marshal(f)
	if Digest(append([]byte(EvidenceSchema+"\n"), unsigned...)) != stringValue(t, fields["content_hash"]) {
		t.Fatal("hash formula")
	}
}
func stringValue(t *testing.T, raw []byte) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEvidenceOwnsEveryMutableValue(t *testing.T) {
	input := fieldsFixture()
	e, err := NewEvidence(input)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := e.CanonicalJSON()
	input.Payload[0] = '!'
	input.SourceLocations[0].Path = "changed"
	input.Artifacts[0].ContentHash = Digest(nil)
	input.InputHashes["source"] = Digest(nil)
	f, _ := e.Fields()
	f.Payload[0] = '!'
	f.SourceLocations[0].Path = "changed"
	f.Artifacts[0].SizeBytes = 100
	f.InputHashes["other"] = Digest(nil)
	out, _ := e.CanonicalJSON()
	out[0] = '!'
	marshaled, _ := e.MarshalJSON()
	marshaled[0] = '!'
	if err := json.Unmarshal(before, &e); !errors.Is(err, ErrImmutableEvidence) {
		t.Fatal("overwrote sealed value", err)
	}
	after, _ := e.CanonicalJSON()
	if !bytes.Equal(before, after) {
		t.Fatal("mutated evidence")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 10 {
				f, err := e.Fields()
				if err != nil {
					t.Error(err)
					return
				}
				f.InputHashes["source"] = "changed"
				raw, _ := e.CanonicalJSON()
				if !bytes.Equal(raw, before) {
					t.Error("concurrent mutation")
				}
			}
		})
	}
	wg.Wait()
}

func TestEvidenceCanonicalizationAndEditionIndependence(t *testing.T) {
	f := fieldsFixture()
	a, _ := NewEvidence(f)
	f.Payload = json.RawMessage(` { "a":9007199254740993, "nested":{"a":1,"b":2},"z":2 } `)
	f.CreatedAt = f.CreatedAt.UTC()
	f.SourceLocations = append(f.SourceLocations, f.SourceLocations[0])
	b, err := NewEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := a.CanonicalJSON()
	got, _ := b.CanonicalJSON()
	if !bytes.Equal(want, got) {
		t.Fatal("canonical equivalent changed")
	}
	for _, offering := range edition.Editions() {
		t.Run(offering.Name, func(t *testing.T) {
			e, err := NewEvidence(fieldsFixture())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := e.CanonicalJSON()
			if !bytes.Equal(raw, want) {
				t.Fatal("edition changed schema")
			}
			if bytes.Contains(raw, []byte(`"edition"`)) {
				t.Fatal("commercial data in evidence")
			}
		})
	}
	f = fieldsFixture()
	f.SourceLocations = nil
	f.Artifacts = nil
	f.InputHashes = nil
	f.Payload = nil
	e, err := NewEvidence(f)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := e.CanonicalJSON()
	for _, fragment := range []string{`"payload":{}`, `"source_locations":[]`, `"artifacts":[]`, `"input_hashes":{}`} {
		if !bytes.Contains(raw, []byte(fragment)) {
			t.Fatal("noncanonical empty", fragment)
		}
	}
}

func TestEvidenceHashBindsEveryField(t *testing.T) {
	e, _ := NewEvidence(fieldsFixture())
	raw, _ := e.CanonicalJSON()
	var base map[string]json.RawMessage
	json.Unmarshal(raw, &base)
	changes := map[string]string{"evidence_id": `"ev_other"`, "tenant_id": `"tenant_other"`, "change_id": `"change_other"`, "run_id": `"run_other"`, "producer": `"other"`, "producer_version": `"2.0.0"`, "category": `"other"`, "type": `"other"`, "deterministic": `false`, "status": `"UNKNOWN"`, "severity": `"WARNING"`, "confidence": `0.1`, "summary": `"Changed"`, "payload": `{}`, "source_locations": `[]`, "artifacts": `[]`, "input_hashes": `{}`, "created_at": `"2026-09-07T00:00:00Z"`, "content_hash": `"` + Digest(nil) + `"`}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			m := map[string]json.RawMessage{}
			for k, v := range base {
				m[k] = v
			}
			m[key] = json.RawMessage(value)
			tampered, _ := json.Marshal(m)
			if _, err := DecodeEvidence(tampered); err == nil {
				t.Fatal("tamper accepted", key)
			}
		})
	}
}

func TestEvidenceValidation(t *testing.T) {
	for name, mutate := range map[string]func(*EvidenceFields){
		"missing tenant": func(f *EvidenceFields) { f.TenantID = "" }, "path identity": func(f *EvidenceFields) { f.EvidenceID = "../escape" }, "producer version": func(f *EvidenceFields) { f.ProducerVersion = "latest" }, "type": func(f *EvidenceFields) { f.Type = "https://scanner" }, "category": func(f *EvidenceFields) { f.Category = "" }, "status": func(f *EvidenceFields) { f.Status = "" }, "severity": func(f *EvidenceFields) { f.Severity = "CRITICAL" }, "pass warning": func(f *EvidenceFields) { f.Severity = SeverityWarning }, "confidence nan": func(f *EvidenceFields) { f.Confidence = math.NaN() }, "confidence inf": func(f *EvidenceFields) { f.Confidence = math.Inf(1) }, "confidence range": func(f *EvidenceFields) { f.Confidence = 1.1 }, "negative confidence": func(f *EvidenceFields) { f.Confidence = -0.1 }, "summary controls": func(f *EvidenceFields) { f.Summary = "PASS\nFAIL" }, "missing date": func(f *EvidenceFields) { f.CreatedAt = time.Time{} }, "invalid date": func(f *EvidenceFields) { f.CreatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, "provided hash": func(f *EvidenceFields) { f.ContentHash = Digest(nil) }, "payload array": func(f *EvidenceFields) { f.Payload = []byte(`[]`) }, "payload null": func(f *EvidenceFields) { f.Payload = []byte(`null`) }, "duplicate payload": func(f *EvidenceFields) { f.Payload = []byte(`{"status":"FAIL","status":"PASS"}`) }, "big payload": func(f *EvidenceFields) { f.Payload = []byte(`{"x":"` + strings.Repeat("x", MaxPayloadBytes) + `"}`) }, "source traversal": func(f *EvidenceFields) { f.SourceLocations[0].Path = "../secret" }, "source url": func(f *EvidenceFields) { f.SourceLocations[0].Path = "http://host/path" }, "source absolute": func(f *EvidenceFields) { f.SourceLocations[0].Path = "/secret" }, "source backslash": func(f *EvidenceFields) { f.SourceLocations[0].Path = `C:\secret` }, "source position": func(f *EvidenceFields) { f.SourceLocations[0].EndLine = 1 }, "source column": func(f *EvidenceFields) { f.SourceLocations[0].StartColumn = 9 }, "artifact hash": func(f *EvidenceFields) { f.Artifacts[0].ContentHash = "bad" }, "artifact size": func(f *EvidenceFields) { f.Artifacts[0].SizeBytes = -1 }, "artifact url": func(f *EvidenceFields) { f.Artifacts[0].ArtifactID = "https://host" }, "duplicate artifact": func(f *EvidenceFields) { f.Artifacts = append(f.Artifacts, f.Artifacts[0]) }, "input hash": func(f *EvidenceFields) { f.InputHashes["source"] = "bad" }, "input key": func(f *EvidenceFields) { f.InputHashes["../source"] = Digest(nil) }, "many sources": func(f *EvidenceFields) { f.SourceLocations = make([]SourceLocation, MaxSourceLocations+1) }, "many artifacts": func(f *EvidenceFields) { f.Artifacts = make([]Artifact, MaxArtifacts+1) },
	} {
		t.Run(name, func(t *testing.T) {
			f := fieldsFixture()
			mutate(&f)
			if _, err := NewEvidence(f); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
	var zero Evidence
	if _, err := zero.CanonicalJSON(); err == nil {
		t.Fatal("zero accepted")
	}
	if _, err := zero.Fields(); err == nil {
		t.Fatal("zero fields")
	}
}

func TestEvidenceStrictDecode(t *testing.T) {
	e, _ := NewEvidence(fieldsFixture())
	raw, _ := e.CanonicalJSON()
	for _, bad := range [][]byte{nil, []byte("null"), []byte("{}"), append(slices.Clone(raw), []byte(" {}")...), bytes.Replace(raw, []byte(`"tenant_id":`), []byte(`"TENANT_ID":`), 1), bytes.Replace(raw, []byte(`"deterministic":true`), []byte(`"deterministic":null`), 1), bytes.Replace(raw, []byte(`"status":"PASS"`), []byte(`"status":"FAIL","status":"PASS"`), 1), bytes.Replace(raw, []byte(`"start_line":2`), []byte(`"START_LINE":2`), 1), bytes.Replace(raw, []byte(`"size_bytes":8`), []byte(`"size_bytes":null`), 1), bytes.Replace(raw, []byte(`"payload":`), []byte(`"edition":"enterprise","payload":`), 1), bytes.Replace(raw, []byte(`"Fixture completed."`), []byte(`"\ud800"`), 1)} {
		if _, err := DecodeEvidence(bad); err == nil {
			t.Fatal("ambiguous wire accepted", string(bad))
		}
	}
}

func TestFromVerificationPreservesStatusAndPayload(t *testing.T) {
	for _, status := range []Status{StatusPass, StatusFail, StatusWarn, StatusError, StatusSkipped, StatusUnavailable, StatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			record := Record{Schema: VerificationSchema, VerifierID: "fixture", VerifierVersion: "1.0.0", Capability: "fixture.check", Subject: Subject{RepositoryID: "repo", Revision: "rev"}, Report: Report{Status: status, Summary: "Fixture result.", Findings: []Finding{{RuleID: "rule", Severity: SeverityInfo, Message: "Location", Path: "main.go", Line: 1}}}, Executions: []Execution{{StepID: "scan", Status: "COMPLETED", ExitCode: 0, StdoutSHA256: Digest(nil), StderrSHA256: Digest(nil), Sandboxed: true}}}
			id := VerificationIdentity{EvidenceID: "ev", TenantID: "tenant", ChangeID: "change", RunID: "run", CreatedAt: time.Now(), Confidence: 0.5}
			e, err := FromVerification(record, id)
			if err != nil {
				t.Fatal(err)
			}
			f, _ := e.Fields()
			if f.Status != status || len(f.SourceLocations) != 1 {
				t.Fatal("status/location lost")
			}
			var restored Record
			if err := json.Unmarshal(f.Payload, &restored); err != nil {
				t.Fatal(err)
			}
			want, _ := record.CanonicalJSON()
			got, _ := restored.CanonicalJSON()
			if !bytes.Equal(want, got) {
				t.Fatal("record lost")
			}
		})
	}
	if _, err := FromVerification(Record{}, VerificationIdentity{}); err == nil {
		t.Fatal("invalid record accepted")
	}
}

func FuzzDecodeEvidence(f *testing.F) {
	e, _ := NewEvidence(fieldsFixture())
	raw, _ := e.CanonicalJSON()
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > MaxEvidenceBytes {
			return
		}
		e, err := DecodeEvidence(raw)
		if err != nil {
			return
		}
		canonical, err := e.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		again, err := DecodeEvidence(canonical)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := again.CanonicalJSON()
		if !bytes.Equal(b, canonical) {
			t.Fatal("unstable canonical evidence")
		}
	})
}
