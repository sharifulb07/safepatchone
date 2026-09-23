package scanners

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

type object map[string]json.RawMessage
type badOutput struct{}

// Only a bounded protocol parser uses this private sentinel. The SDK also
// contains extension panics; no arbitrary scanner text is returned in errors.
func invalid() { panic(badOutput{}) }
func obj(raw json.RawMessage) object {
	var v object
	if json.Unmarshal(raw, &v) != nil || v == nil {
		invalid()
	}
	return v
}
func field(o object, key string) json.RawMessage {
	for k := range o {
		if k != key && strings.EqualFold(k, key) {
			invalid()
		}
	}
	return o[key]
}
func list(raw json.RawMessage, optional bool) []json.RawMessage {
	if optional && (len(raw) == 0 || bytes.Equal(raw, []byte("null"))) {
		return nil
	}
	var v []json.RawMessage
	if json.Unmarshal(raw, &v) != nil || v == nil || len(v) > 100000 {
		invalid()
	}
	return v
}
func str(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) != nil || !evidence.ValidText(v, 4096) {
		invalid()
	}
	return v
}
func integer(raw json.RawMessage) int {
	var v int
	if json.Unmarshal(raw, &v) != nil || v < 0 {
		invalid()
	}
	return v
}
func optionalStr(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte(`""`)) {
		return ""
	}
	return str(raw)
}

func (a *adapter) Normalize(ctx context.Context, input verification.NormalizationRequest) (report evidence.Report, err error) {
	report = evidence.Report{Status: evidence.StatusUnknown, Summary: "Scanner output did not establish a completed scan.", Findings: []evidence.Finding{}}
	defer func() {
		if p := recover(); p != nil {
			if _, ok := p.(badOutput); !ok {
				panic(p)
			}
			report.Status = evidence.StatusError
			err = ErrOutput
		}
	}()
	if ctx == nil || input.Target.Validate() != nil || input.Capability != a.Capabilities()[0] || len(input.Results) != 1 || input.Results[0].StepID != "scan" {
		return report, ErrOutput
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	r := input.Results[0].Result
	if r.StdoutTruncated || r.StderrTruncated || len(r.Stdout) > int(verification.MaxCaptureBytes) || len(r.Stderr) > int(verification.MaxCaptureBytes) {
		return report, ErrOutput
	}
	if (r.Status != verification.Completed && r.Status != verification.Failed) || (r.Status == verification.Completed) != (r.ExitCode == 0) {
		return report, ErrOutput
	}
	if r.ExitCode != 0 && (r.ExitCode != 1 || a.tool == Syft) {
		report.Status = evidence.StatusError
		report.Summary = "Scanner execution failed."
		return report, nil
	}
	// Wrap arrays too, reusing the shared strict Unicode/duplicate/depth validator.
	raw := append([]byte(`{"payload":`), r.Stdout...)
	raw = append(raw, '}')
	validated, parseErr := jsonobject.Normalize(raw, int(verification.MaxCaptureBytes)+32)
	if parseErr != nil {
		return report, ErrOutput
	}
	payload := field(obj(validated), "payload")
	complete := true
	count := 0
	add := func(rule, file string, line int, message string, severity evidence.Severity) {
		if !evidence.ValidText(rule, 4096) || len(report.Findings) >= evidence.MaxFindings {
			invalid()
		}
		// Do not copy scanner messages, snippets, matches, secrets or host paths.
		// A stable hash handles upstream rule IDs outside SafePatch's vocabulary.
		id := string(a.tool) + "." + evidence.Digest([]byte(rule))[:32]
		file = strings.TrimPrefix(strings.ReplaceAll(file, "\\", "/"), "./")
		if !evidence.ValidPath(file) {
			file = ""
			line = 0
		}
		report.Findings = append(report.Findings, evidence.Finding{RuleID: id, Path: file, Line: line, Message: message, Severity: severity})
	}
	switch a.tool {
	case Gitleaks:
		items := list(payload, false)
		for _, item := range items {
			o := obj(item)
			add(str(field(o, "RuleID")), str(field(o, "File")), integer(field(o, "StartLine")), "Gitleaks detected a potential secret. Consult restricted original evidence.", evidence.SeverityError)
		}
	case Semgrep:
		o := obj(payload)
		if str(field(o, "version")) != a.config.Version {
			invalid()
		}
		if len(list(field(o, "errors"), false)) > 0 {
			complete = false
		}
		paths := obj(field(o, "paths"))
		scanned := list(field(paths, "scanned"), false)
		count = len(scanned)
		for _, f := range scanned {
			str(f)
		}
		if len(list(field(paths, "skipped"), true)) > 0 {
			complete = false
		}
		for _, item := range list(field(o, "results"), false) {
			f := obj(item)
			start := obj(field(f, "start"))
			extra := obj(field(f, "extra"))
			severity := str(field(extra, "severity"))
			if severity != "ERROR" && severity != "WARNING" && severity != "INFO" && severity != "INVENTORY" && severity != "EXPERIMENT" {
				invalid()
			}
			add(str(field(f, "check_id")), str(field(f, "path")), integer(field(start, "line")), "Semgrep rule matched. Consult original evidence for rule details.", evidence.SeverityError)
		}
	case Trivy:
		o := obj(payload)
		if integer(field(o, "SchemaVersion")) != 2 || str(field(o, "ArtifactType")) != "filesystem" {
			invalid()
		}
		for _, item := range list(field(o, "Results"), true) {
			result := obj(item)
			target := str(field(result, "Target"))
			for _, item := range list(field(result, "Packages"), true) {
				pkg := obj(item)
				str(field(pkg, "Name"))
				count++
			}
			for _, item := range list(field(result, "Vulnerabilities"), true) {
				v := obj(item)
				id := str(field(v, "VulnerabilityID"))
				str(field(v, "PkgName"))
				add(id, target, 0, "Trivy reported a vulnerable package. Consult original evidence for advisory and fix details.", evidence.SeverityError)
			}
		}
	case OSV:
		o := obj(payload)
		for _, item := range list(field(o, "results"), false) {
			result := obj(item)
			source := obj(field(result, "source"))
			file := str(field(source, "path"))
			str(field(source, "type"))
			for _, item := range list(field(result, "packages"), false) {
				pkg := obj(item)
				name := str(field(obj(field(pkg, "package")), "name"))
				count++
				for _, item := range list(field(pkg, "vulnerabilities"), true) {
					v := obj(item)
					add(str(field(v, "id")), file, 0, "OSV reported a vulnerable package ("+safeName(name)+"). Consult original evidence for advisory details.", evidence.SeverityError)
				}
			}
		}
	case Syft:
		o := obj(payload)
		schema := obj(field(o, "schema"))
		version := str(field(schema, "version"))
		if version != "16.0.0" && version != "16.1.0" {
			invalid()
		}
		descriptor := obj(field(o, "descriptor"))
		if str(field(descriptor, "name")) != "syft" || str(field(descriptor, "version")) != a.config.Version {
			invalid()
		}
		source := obj(field(o, "source"))
		if str(field(source, "type")) != "directory" {
			invalid()
		}
		for _, item := range list(field(o, "artifacts"), false) {
			p := obj(item)
			id := str(field(p, "id"))
			name := str(field(p, "name"))
			str(field(p, "type"))
			count++
			add(id, "", 0, "Syft inventoried a package ("+safeName(name)+"). The original SBOM retains package metadata.", evidence.SeverityInfo)
		}
	}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	findings := len(report.Findings) > 0 && a.tool != Syft
	if (r.ExitCode == 1) != findings {
		return report, ErrOutput
	}
	// Diagnostics can mean a partial scan even when a tool exits zero. Preserve
	// them, but do not guess whether warning text is harmless or evidence of loss.
	if !complete || len(bytes.TrimSpace(r.Stderr)) > 0 {
		report.Status = evidence.StatusUnknown
		return report, nil
	}
	if findings {
		report.Status = evidence.StatusFail
		report.Summary = "Scanner reported findings within its configured scope."
	} else if count == 0 && a.tool != Gitleaks {
		report.Status = evidence.StatusSkipped
		report.Summary = "Scanner reported no scanned files or packages."
	} else {
		report.Status = evidence.StatusPass
		report.Summary = "Scanner completed within its configured scope."
	}
	if a.tool == Syft && report.Status == evidence.StatusPass {
		report.Summary = "SBOM generation completed; this is inventory evidence, not a vulnerability verdict."
	}
	return report.Canonical()
}

// Keep arbitrary package strings out of ordinary reports while making distinct
// packages distinguishable. Original bytes remain in the restricted archive.
func safeName(name string) string {
	return fmt.Sprintf("sha256:%s", evidence.Digest([]byte(name))[:16])
}
