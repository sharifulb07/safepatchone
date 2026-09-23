package evidence

import "time"

// VerificationIdentity is trusted run provenance and assessment policy. It is
// supplied after tenant/actor admission and never populated from scanner JSON.
type VerificationIdentity struct {
	EvidenceID    string
	TenantID      string
	ChangeID      string
	RunID         string
	CreatedAt     time.Time
	Deterministic bool
	Confidence    float64
	Artifacts     []Artifact
	InputHashes   map[string]string
}

// FromVerification preserves the complete existing canonical Record as payload.
// It adds immutable evidence identity without replacing the SDK wire contract.
// No status conversion or edition-specific behavior occurs here.
func FromVerification(record Record, identity VerificationIdentity) (Evidence, error) {
	raw, err := record.CanonicalJSON()
	if err != nil {
		return Evidence{}, err
	}
	severity := SeverityInfo
	sources := []SourceLocation{}
	for _, f := range record.Report.Findings {
		if f.Severity == SeverityError {
			severity = SeverityError
		} else if f.Severity == SeverityWarning && severity != SeverityError {
			severity = SeverityWarning
		}
		if f.Path != "" {
			sources = append(sources, SourceLocation{Path: f.Path, StartLine: f.Line, StartColumn: f.Column})
		}
	}
	if record.Report.Status == StatusFail || record.Report.Status == StatusError {
		severity = SeverityError
	} else if record.Report.Status == StatusWarn && severity == SeverityInfo {
		severity = SeverityWarning
	}
	return NewEvidence(EvidenceFields{EvidenceID: identity.EvidenceID, TenantID: identity.TenantID, ChangeID: identity.ChangeID, RunID: identity.RunID, Producer: record.VerifierID, ProducerVersion: record.VerifierVersion, Category: "verification", Type: record.Capability, Deterministic: identity.Deterministic, Status: record.Report.Status, Severity: severity, Confidence: identity.Confidence, Summary: record.Report.Summary, Payload: raw, SourceLocations: sources, Artifacts: identity.Artifacts, InputHashes: identity.InputHashes, CreatedAt: identity.CreatedAt})
}
