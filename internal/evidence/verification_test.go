package evidence

import (
	"bytes"
	"strings"
	"testing"
)

func TestReportStatusesAndStrictDecoding(t *testing.T) {
	for _, status := range []Status{StatusPass, StatusFail, StatusWarn, StatusError, StatusSkipped, StatusUnavailable, StatusUnknown} {
		raw := []byte(`{"status":"` + string(status) + `","summary":"Result","findings":[]}`)
		r, err := DecodeReport(raw)
		if err != nil || r.Status != status || r.Findings == nil {
			t.Fatal(r, err)
		}
	}
	for _, raw := range []string{
		``, `{}`, `{"status":"UNKNOWN","status":"PASS","summary":"ok"}`,
		`{"status":"UNKNOWN","STATUS":"PASS","summary":"ok"}`,
		`{"status":"PASS","summary":"ok","findings":[{"rule_id":"r","severity":"ERROR","SEVERITY":"INFO","message":"bad"}]}`,
		`{"status":"PASS","summary":"ok","verifier_id":"trusted"}`,
		`{"status":"PASS","summary":"ok"} {}`, `{"status":"pass","summary":"ok"}`,
		`{"status":"PASS","summary":"ok","findings":[{"rule_id":"r","severity":"ERROR","message":"bad"}]}`,
		`{"status":"PASS","summary":"ok\u001b[2J"}`,
		`{"status":"PASS","summary":"\ud800"}`,
		strings.Repeat("x", MaxEvidenceBytes+1),
	} {
		if _, err := DecodeReport([]byte(raw)); err == nil {
			t.Errorf("poisoned report accepted: %.100q", raw)
		}
	}
}

func TestFindingPathsBoundsAndCanonicalization(t *testing.T) {
	f := Finding{RuleID: "rule", Severity: SeverityError, Message: "issue", Path: "src/main.go", Line: 12, Column: 2}
	r := Report{Status: StatusFail, Summary: "Findings", Findings: []Finding{f, f, {RuleID: "other", Severity: SeverityInfo, Message: "note"}}}
	a, err := r.Canonical()
	if err != nil || len(a.Findings) != 2 || len(r.Findings) != 3 {
		t.Fatal(a, err)
	}
	for _, bad := range []string{"../secret", "/etc/passwd", `C:\secret`, "x/../../y", "a//b", "a/./b", "file\nPASS", "https://metadata/secret"} {
		f.Path = bad
		r.Findings = []Finding{f}
		if _, err := r.Canonical(); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	f.Path = ""
	r.Findings = []Finding{f}
	if _, err := r.Canonical(); err == nil {
		t.Fatal("line without location accepted")
	}
	r.Findings = make([]Finding, MaxFindings+1)
	if _, err := r.Canonical(); err == nil {
		t.Fatal("unbounded findings")
	}
}

func TestCanonicalEvidenceHasStableIdentityAndNoEmptyPass(t *testing.T) {
	r := Record{Schema: VerificationSchema, VerifierID: "test", VerifierVersion: "1.0.0", Capability: "test", Subject: Subject{"repo", "revision"}, Report: Report{Status: StatusPass, Summary: "Complete"}, Executions: []Execution{{StepID: "test", Status: "COMPLETED", ExitCode: 0, StdoutSHA256: Digest([]byte("output")), StderrSHA256: Digest(nil)}}}
	a, err := r.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.CanonicalJSON()
	if err != nil || !bytes.Equal(a, b) || Digest(a) != Digest(b) || !bytes.Contains(a, []byte(`"findings":[]`)) {
		t.Fatal(string(b), err)
	}
	r.Executions[0].Status = "UNKNOWN"
	if _, err := r.CanonicalJSON(); err == nil {
		t.Fatal("UNKNOWN became PASS")
	}
	r.Executions = nil
	if _, err := r.CanonicalJSON(); err == nil {
		t.Fatal("empty execution became PASS")
	}
	if bytes.Contains(a, []byte(`"edition"`)) || bytes.Contains(a, []byte(`"environment"`)) {
		t.Fatal("commercial or secret data in evidence")
	}
}
