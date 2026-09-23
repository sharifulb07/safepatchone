package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"safepatch/internal/tenant"
)

type Decision string

const Match Decision = "MATCH"
const Different Decision = "DIFFERENT"
const Unknown Decision = "UNKNOWN"

// Sandbox executes a trusted configured candidate inside OS isolation, with
// network disabled and only temporary local writes. It must never forward live
// dependencies. Stopped is infrastructure evidence, never scanner output.
type Execution struct {
	Observation Observation
	Stopped     bool
}
type Sandbox interface {
	Execute(context.Context, Record) (Execution, error)
}

// Observer correlates only IDs/digests/status. Raw inputs, outputs, errors and
// baggage must not be attached. Source spans are links, never authorization.
type Observer interface {
	Start(context.Context, Scope, string, string, TraceLink) (context.Context, func(Decision))
}
type CaseResult struct {
	RecordID string   `json:"record_id"`
	Decision Decision `json:"decision"`
	Code     string   `json:"code"`
}
type Result struct {
	DatasetDigest string       `json:"dataset_digest"`
	Decision      Decision     `json:"decision"`
	Cases         []CaseResult `json:"cases"`
}
type Engine struct {
	sandbox  Sandbox
	redactor *Redactor
	observer Observer
}

func NewEngine(s Sandbox, r *Redactor, o Observer) (*Engine, error) {
	if s == nil || r.Digest() == "" || o == nil {
		return nil, ErrInvalid
	}
	return &Engine{s, r, o}, nil
}

// SideEffectSandbox permits only recorded dependency fixtures. It cannot
// establish OS isolation itself; the injected execution adapter must enforce it.
type SideEffectSandbox struct{}

func (SideEffectSandbox) Check(record Record) error {
	ids := map[string]bool{}
	for _, f := range record.Fixtures {
		if ids[f.ID] || !tenant.ValidID(f.ID) || (f.Effect != Pure && f.Effect != Read && f.Effect != Write) {
			return ErrDenied
		}
		ids[f.ID] = true
	}
	for _, id := range record.Expected.Effects {
		if !ids[id] {
			return ErrDenied
		}
	}
	return nil
}
func Compare(expected, actual Observation) Decision {
	if !expected.Complete || !actual.Complete || expected.Status <= 0 || actual.Status <= 0 || expected.Status > 599 || actual.Status > 599 {
		return Unknown
	}
	a, e := canonical(expected.Output, MaxPayloadBytes)
	b, f := canonical(actual.Output, MaxPayloadBytes)
	if e != nil || f != nil {
		return Unknown
	}
	if expected.Status != actual.Status || !bytes.Equal(a, b) || !slices.Equal(expected.Effects, actual.Effects) {
		return Different
	}
	return Match
}
func (e *Engine) Run(ctx context.Context, d ReplayDataset, run string) (Result, error) {
	result := Result{DatasetDigest: d.Digest(), Decision: Unknown, Cases: []CaseResult{}}
	if e == nil || ctx == nil || !tenant.ValidID(run) {
		return result, ErrInvalid
	}
	f, err := d.Fields()
	if err != nil || f.RedactionDigest != e.redactor.Digest() {
		return result, ErrInvalid
	}
	unknown, different := false, false
	for _, record := range f.Records {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		caseContext, end := e.observer.Start(ctx, f.Scope, run, record.ID, record.SourceTrace)
		if caseContext == nil || end == nil {
			return result, ErrUnavailable
		}
		cr := CaseResult{RecordID: record.ID, Decision: Unknown, Code: "UNAVAILABLE"}
		if (SideEffectSandbox{}).Check(record) != nil {
			cr.Code = "SIDE_EFFECT_BLOCKED"
			unknown = true
			end(Unknown)
			result.Cases = append(result.Cases, cr)
			continue
		}
		execution, err := e.sandbox.Execute(caseContext, record)
		if !execution.Stopped {
			end(Unknown)
			return result, ErrUnsafe
		}
		if err == nil && caseContext.Err() == nil {
			actual, er := redactObservation(execution.Observation, e.redactor)
			if er == nil {
				// Unrecorded effects indicate incomplete isolation instrumentation.
				for _, id := range actual.Effects {
					found := false
					for _, fixture := range record.Fixtures {
						if fixture.ID == id {
							found = true
						}
					}
					if !found {
						actual.Complete = false
					}
				}
				cr.Decision = Compare(record.Expected, actual)
				cr.Code = "COMPARED"
			}
		}
		end(cr.Decision)
		result.Cases = append(result.Cases, cr)
		unknown = unknown || cr.Decision == Unknown
		different = different || cr.Decision == Different
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if !unknown && len(result.Cases) > 0 {
		result.Decision = Match
		if different {
			result.Decision = Different
		}
	}
	return result, nil
}

// DecodeObservation rejects ambiguous output from the candidate protocol.
func DecodeObservation(raw []byte) (Observation, error) {
	checked, err := canonical(raw, MaxPayloadBytes)
	if err != nil {
		return Observation{}, err
	}
	var o Observation
	if json.Unmarshal(checked, &o) != nil {
		return o, ErrInvalid
	}
	encoded, _ := json.Marshal(o)
	want, _ := canonical(encoded, MaxPayloadBytes)
	if !bytes.Equal(checked, want) {
		return Observation{}, ErrInvalid
	}
	return o, nil
}
