package golang

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"safepatch/internal/domain/jsonobject"
	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

func objects(raw []byte, consume func(map[string]json.RawMessage, json.RawMessage) error) error {
	if int64(len(raw)) > verification.MaxCaptureBytes {
		return evidence.ErrInvalidEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	count := 0
	for {
		var item json.RawMessage
		err := decoder.Decode(&item)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return evidence.ErrInvalidEvidence
		}
		count++
		if count > 100000 {
			return evidence.ErrInvalidEvidence
		}
		clean, err := jsonobject.Normalize(item, evidence.MaxEvidenceBytes)
		if err != nil {
			return evidence.ErrInvalidEvidence
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(clean, &fields) != nil {
			return evidence.ErrInvalidEvidence
		}
		if err := consume(fields, clean); err != nil {
			return err
		}
	}
}
func exact(fields map[string]json.RawMessage, allowed ...string) error {
	for key := range fields {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
			}
		}
		if !found {
			return evidence.ErrInvalidEvidence
		}
	}
	return nil
}
func (v *Verifier) normalize(cap verification.Capability, r verification.ExecutionResult) (evidence.Report, error) {
	switch cap {
	case Test, Race, Fuzz, Benchmark:
		return testReport(cap, r)
	case Staticcheck:
		return staticReport(r)
	case Vulncheck:
		return v.vulnerabilityReport(r)
	case Vet:
		return vetReport(r)
	case Build:
		failed := false
		err := objects(r.Stdout, func(fields map[string]json.RawMessage, raw json.RawMessage) error {
			if exact(fields, "ImportPath", "Action", "Output") != nil {
				return evidence.ErrInvalidEvidence
			}
			var event struct{ ImportPath, Action, Output string }
			if json.Unmarshal(raw, &event) != nil {
				return evidence.ErrInvalidEvidence
			}
			switch event.Action {
			case "build-output":
			case "build-fail":
				failed = true
			default:
				return evidence.ErrInvalidEvidence
			}
			return nil
		})
		if err != nil {
			return evidence.Report{}, err
		}
		if failed && r.ExitCode == 0 {
			return evidence.Report{}, evidence.ErrInvalidEvidence
		}
		status := evidence.StatusPass
		if r.ExitCode != 0 {
			status = evidence.StatusFail
		} else if len(bytes.TrimSpace(r.Stderr)) != 0 {
			status = evidence.StatusUnknown
		}
		return evidence.Report{Status: status, Summary: "Go build result"}, nil
	default:
		return evidence.Report{}, verification.ErrInvalidVerifier
	}
}

type testEvent struct {
	Action, Package, Test, Output, ImportPath, FailedBuild string
	Elapsed                                                float64
	Time                                                   string
}

var benchName = regexp.MustCompile(`^Benchmark[A-Za-z0-9_/=-]+(?:-[0-9]+)?$`)

func testReport(cap verification.Capability, r verification.ExecutionResult) (evidence.Report, error) {
	report := evidence.Report{Status: evidence.StatusPass, Summary: "Go test result", Findings: []evidence.Finding{}}
	started := map[string]bool{}
	terminal := map[string]string{}
	runs := map[string]bool{}
	finished := map[string]bool{}
	fragments := map[string]string{}
	measured := map[string]bool{}
	passed, failed, benchmarks, fuzzed := 0, false, 0, false
	err := objects(r.Stdout, func(fields map[string]json.RawMessage, raw json.RawMessage) error {
		if exact(fields, "Time", "Action", "Package", "Test", "Elapsed", "Output", "ImportPath", "FailedBuild") != nil {
			return evidence.ErrInvalidEvidence
		}
		var event testEvent
		if json.Unmarshal(raw, &event) != nil || math.IsNaN(event.Elapsed) || math.IsInf(event.Elapsed, 0) || event.Elapsed < 0 {
			return evidence.ErrInvalidEvidence
		}
		if event.Action == "build-output" || event.Action == "build-fail" {
			if event.Action == "build-fail" {
				failed = true
			}
			return nil
		}
		if !evidence.ValidText(event.Package, 512) {
			return evidence.ErrInvalidEvidence
		}
		key := event.Package + "\x00" + event.Test
		switch event.Action {
		case "start":
			if started[event.Package] || event.Test != "" {
				return evidence.ErrInvalidEvidence
			}
			started[event.Package] = true
		case "run":
			if event.Test == "" || runs[key] || !started[event.Package] {
				return evidence.ErrInvalidEvidence
			}
			runs[key] = true
		case "pass", "fail", "skip":
			if event.Test == "" {
				if !started[event.Package] || terminal[event.Package] != "" {
					return evidence.ErrInvalidEvidence
				}
				terminal[event.Package] = event.Action
			} else {
				if !runs[key] || finished[key] {
					return evidence.ErrInvalidEvidence
				}
				finished[key] = true
				if event.Action == "pass" {
					passed++
					if strings.HasPrefix(event.Test, "Fuzz") {
						fuzzed = true
					}
				}
			}
			if event.Action == "fail" {
				failed = true
				name := event.Test
				if name == "" {
					name = "package"
				}
				report.Findings = append(report.Findings, evidence.Finding{RuleID: "go.test.failure", Severity: evidence.SeverityError, Message: fmt.Sprintf("%s failed in %s", name, event.Package)})
			}
		case "output":
			if !started[event.Package] {
				return evidence.ErrInvalidEvidence
			}
			if cap == Benchmark {
				fragments[key] += event.Output
				if len(fragments[key]) > 8192 {
					return evidence.ErrInvalidEvidence
				}
				for strings.Contains(fragments[key], "\n") {
					line, rest, _ := strings.Cut(fragments[key], "\n")
					fragments[key] = rest
					fields := strings.Fields(line)
					if len(fields) >= 4 && benchName.MatchString(fields[0]) {
						if !runs[key] || !strings.HasPrefix(event.Test, "Benchmark") {
							return evidence.ErrInvalidEvidence
						}
						iterations, err := strconv.ParseInt(fields[1], 10, 64)
						if err != nil || iterations < 1 {
							return evidence.ErrInvalidEvidence
						}
						if (len(fields)-2)%2 != 0 {
							return evidence.ErrInvalidEvidence
						}
						for i := 2; i < len(fields); i += 2 {
							value, err := strconv.ParseFloat(fields[i], 64)
							if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || !evidence.ValidText(fields[i+1], 64) {
								return evidence.ErrInvalidEvidence
							}
						}
						benchmarks++
						measured[key] = true
						report.Findings = append(report.Findings, evidence.Finding{RuleID: "go.benchmark", Severity: evidence.SeverityInfo, Message: event.Package + ": " + strings.Join(fields, " ")})
					}
				}
			}
		case "pause", "cont", "bench":
			if !started[event.Package] {
				return evidence.ErrInvalidEvidence
			}
		default:
			return evidence.ErrInvalidEvidence
		}
		if len(report.Findings) > evidence.MaxFindings {
			return evidence.ErrInvalidEvidence
		}
		return nil
	})
	if err != nil {
		return evidence.Report{}, err
	}
	if r.ExitCode != 0 {
		report.Status = evidence.StatusFail
		return report, nil
	}
	if failed {
		return evidence.Report{}, evidence.ErrInvalidEvidence
	}
	if cap == Benchmark {
		for _, fragment := range fragments {
			if strings.TrimSpace(fragment) != "" {
				report.Status = evidence.StatusUnknown
				return report, nil
			}
		}
	}
	if len(started) == 0 || len(terminal) != len(started) || len(bytes.TrimSpace(r.Stderr)) != 0 {
		report.Status = evidence.StatusUnknown
		return report, nil
	}
	for key := range runs {
		if !finished[key] {
			covered := false
			if cap == Benchmark {
				for benchmark := range measured {
					if benchmark == key || strings.HasPrefix(benchmark, key+"/") {
						covered = true
						break
					}
				}
			}
			if covered {
				continue
			}
			report.Status = evidence.StatusUnknown
			return report, nil
		}
	}
	switch cap {
	case Benchmark:
		if benchmarks == 0 {
			report.Status = evidence.StatusSkipped
		}
	case Fuzz:
		if !fuzzed {
			report.Status = evidence.StatusUnknown
		}
	default:
		if passed == 0 {
			report.Status = evidence.StatusSkipped
		}
	}
	return report, nil
}

func staticReport(r verification.ExecutionResult) (evidence.Report, error) {
	report := evidence.Report{Status: evidence.StatusPass, Summary: "Staticcheck result", Findings: []evidence.Finding{}}
	err := objects(r.Stdout, func(fields map[string]json.RawMessage, raw json.RawMessage) error {
		if exact(fields, "code", "severity", "location", "end", "message", "related") != nil {
			return evidence.ErrInvalidEvidence
		}
		var issue struct {
			Code, Severity, Message string
			Location                struct {
				File         string
				Line, Column int
			}
		}
		if json.Unmarshal(raw, &issue) != nil || !evidence.ValidIdentifier(issue.Code) || !evidence.ValidText(issue.Message, 4096) {
			return evidence.ErrInvalidEvidence
		}
		severity := evidence.SeverityError
		switch issue.Severity {
		case "error":
			report.Status = evidence.StatusFail
		case "warning", "ignored":
			severity = evidence.SeverityWarning
			if report.Status == evidence.StatusPass {
				report.Status = evidence.StatusWarn
			}
		default:
			return evidence.ErrInvalidEvidence
		}
		// Absolute scanner paths refer to ephemeral runner directories. Omit
		// them rather than guessing a repository mapping or leaking host paths.
		file := strings.TrimPrefix(issue.Location.File, "./")
		line, column := issue.Location.Line, issue.Location.Column
		if !evidence.ValidPath(file) {
			file = ""
			line = 0
			column = 0
		}
		report.Findings = append(report.Findings, evidence.Finding{RuleID: issue.Code, Severity: severity, Message: issue.Message, Path: file, Line: line, Column: column})
		if len(report.Findings) > evidence.MaxFindings {
			return evidence.ErrInvalidEvidence
		}
		return nil
	})
	if err != nil {
		return evidence.Report{}, err
	}
	if len(bytes.TrimSpace(r.Stderr)) != 0 || r.ExitCode != 0 && len(report.Findings) == 0 {
		report.Status = evidence.StatusError
	}
	return report, nil
}

func vetReport(r verification.ExecutionResult) (evidence.Report, error) {
	report := evidence.Report{Status: evidence.StatusPass, Summary: "Go vet result", Findings: []evidence.Finding{}}
	var body bytes.Buffer
	combined := append(append(append([]byte{}, r.Stdout...), '\n'), r.Stderr...)
	for _, line := range bytes.Split(combined, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("# ")) {
			continue
		}
		body.Write(line)
		body.WriteByte('\n')
	}
	err := objects(body.Bytes(), func(packages map[string]json.RawMessage, _ json.RawMessage) error {
		for _, analyzers := range packages {
			var entries map[string]json.RawMessage
			if json.Unmarshal(analyzers, &entries) != nil {
				return evidence.ErrInvalidEvidence
			}
			for analyzer, raw := range entries {
				if !evidence.ValidIdentifier(analyzer) {
					return evidence.ErrInvalidEvidence
				}
				var findings []struct{ Posn, Message string }
				if json.Unmarshal(raw, &findings) != nil {
					return evidence.ErrInvalidEvidence
				}
				for _, finding := range findings {
					if !evidence.ValidText(finding.Message, 4096) {
						return evidence.ErrInvalidEvidence
					}
					report.Status = evidence.StatusFail
					file, line := location(finding.Posn)
					report.Findings = append(report.Findings, evidence.Finding{RuleID: "go.vet." + analyzer, Severity: evidence.SeverityError, Message: finding.Message, Path: file, Line: line})
					if len(report.Findings) > evidence.MaxFindings {
						return evidence.ErrInvalidEvidence
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return evidence.Report{}, err
	}
	if r.ExitCode != 0 && len(report.Findings) == 0 {
		report.Status = evidence.StatusError
	}
	return report, nil
}

func (v *Verifier) vulnerabilityReport(r verification.ExecutionResult) (evidence.Report, error) {
	report := evidence.Report{Status: evidence.StatusPass, Summary: "Govulncheck result", Findings: []evidence.Finding{}}
	configured, scanned := false, false
	err := objects(r.Stdout, func(fields map[string]json.RawMessage, _ json.RawMessage) error {
		if len(fields) != 1 || exact(fields, "config", "progress", "SBOM", "osv", "finding") != nil {
			return evidence.ErrInvalidEvidence
		}
		if raw, ok := fields["config"]; ok {
			if configured {
				return evidence.ErrInvalidEvidence
			}
			var keys map[string]json.RawMessage
			if json.Unmarshal(raw, &keys) != nil || exact(keys, "protocol_version", "scanner_name", "scanner_version", "db", "db_last_modified", "go_version", "scan_level", "scan_mode") != nil {
				return evidence.ErrInvalidEvidence
			}
			var config struct {
				ProtocolVersion string `json:"protocol_version"`
				ScannerName     string `json:"scanner_name"`
				ScanLevel       string `json:"scan_level"`
				DB              string `json:"db"`
			}
			if json.Unmarshal(raw, &config) != nil || config.ProtocolVersion != "v1.0.0" || config.ScannerName != "govulncheck" || config.ScanLevel != "symbol" || config.DB != v.config.VulnerabilityDB {
				return evidence.ErrInvalidEvidence
			}
			configured = true
			return nil
		}
		if !configured {
			return evidence.ErrInvalidEvidence
		}
		if raw, ok := fields["SBOM"]; ok {
			var keys map[string]json.RawMessage
			if json.Unmarshal(raw, &keys) != nil || exact(keys, "go_version", "modules", "roots") != nil {
				return evidence.ErrInvalidEvidence
			}
			var sbom struct {
				Roots []string `json:"roots"`
			}
			if scanned || json.Unmarshal(raw, &sbom) != nil || len(sbom.Roots) == 0 {
				return evidence.ErrInvalidEvidence
			}
			scanned = true
		}
		if raw, ok := fields["finding"]; ok {
			var keys map[string]json.RawMessage
			if json.Unmarshal(raw, &keys) != nil || exact(keys, "osv", "fixed_version", "trace") != nil {
				return evidence.ErrInvalidEvidence
			}
			var frames []map[string]json.RawMessage
			if json.Unmarshal(keys["trace"], &frames) != nil {
				return evidence.ErrInvalidEvidence
			}
			for _, frame := range frames {
				if exact(frame, "module", "version", "package", "function", "receiver", "position") != nil {
					return evidence.ErrInvalidEvidence
				}
			}
			var finding struct {
				OSV          string                                                `json:"osv"`
				FixedVersion string                                                `json:"fixed_version"`
				Trace        []struct{ Module, Version, Package, Function string } `json:"trace"`
			}
			if json.Unmarshal(raw, &finding) != nil || !evidence.ValidIdentifier(finding.OSV) || len(finding.Trace) == 0 {
				return evidence.ErrInvalidEvidence
			}
			severity := evidence.SeverityWarning
			status := evidence.StatusWarn
			for _, frame := range finding.Trace {
				if frame.Function != "" {
					severity = evidence.SeverityError
					status = evidence.StatusFail
				}
			}
			if rank(status) > rank(report.Status) {
				report.Status = status
			}
			message := fmt.Sprintf("%s in %s; fixed version: %s", finding.OSV, finding.Trace[0].Module, finding.FixedVersion)
			report.Findings = append(report.Findings, evidence.Finding{RuleID: finding.OSV, Severity: severity, Message: message})
			if len(report.Findings) > evidence.MaxFindings {
				return evidence.ErrInvalidEvidence
			}
		}
		return nil
	})
	if err != nil {
		return evidence.Report{}, err
	}
	if !configured || !scanned || r.ExitCode != 0 || len(bytes.TrimSpace(r.Stderr)) != 0 {
		report.Status = evidence.StatusError
	}
	return report, nil
}
