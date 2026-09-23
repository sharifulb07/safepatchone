package replay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"safepatch/internal/replay"
)

func fixture(t testing.TB) (*replay.Redactor, replay.ReplayDataset) {
	t.Helper()
	r, err := replay.NewRedactor([]string{"/value", "/token", "/nested/status"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	d, err := replay.NewDataset(replay.Scope{OrganizationID: "org", RepositoryID: "repo"}, "dataset", now, now.Add(24*time.Hour), []replay.Record{{ID: "case", Input: json.RawMessage(`{"value":1,"token":"secret-token","password":"private","email":"user@example.com","nested":{"status":"ok","private":"hidden"}}`), Expected: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"value":2,"private":"baseline-secret"}`)}, SourceTrace: replay.TraceLink{TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16)}}}, r)
	if err != nil {
		t.Fatal(err)
	}
	return r, d
}
func TestRedactionBeforeSealingAndDecode(t *testing.T) {
	r, d := fixture(t)
	raw, _ := d.MarshalJSON()
	for _, secret := range []string{"secret-token", "private", "password", "user@example.com", "baseline-secret", "hidden"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("privacy leak", secret)
		}
	}
	if !bytes.Contains(raw, []byte("[REDACTED]")) {
		t.Fatal("secret-name override missing")
	}
	copy, err := replay.DecodeDataset(raw, r)
	if err != nil || copy.Digest() != d.Digest() {
		t.Fatal(err)
	}
	fields, _ := d.Fields()
	fields.Records[0].Input[0] = '!'
	again, _ := d.MarshalJSON()
	if !bytes.Equal(raw, again) {
		t.Fatal("mutable dataset")
	}
	other, _ := replay.NewRedactor([]string{"/value"})
	if _, err := replay.DecodeDataset(raw, other); err == nil {
		t.Fatal("privacy policy drift")
	}
	for _, bad := range [][]byte{[]byte(`{"value":1,"value":2}`), []byte(`{"value":{"secret":"x"}}`), []byte(`{"value":"\ud800"}`), bytes.Repeat([]byte{' '}, replay.MaxPayloadBytes+1)} {
		if _, err := r.Redact(bad); err == nil {
			t.Fatal("unsafe redaction input")
		}
	}
	redacted, err := r.Redact([]byte(`{"value":"Bearer private-access-token"}`))
	if err != nil || bytes.Contains(redacted, []byte("private-access")) {
		t.Fatal("recognizable credential leak")
	}
}

type sandbox struct {
	observation replay.Observation
	err         error
	stopped     bool
	calls       int
}

func (s *sandbox) Execute(context.Context, replay.Record) (replay.Execution, error) {
	s.calls++
	return replay.Execution{Observation: s.observation, Stopped: s.stopped}, s.err
}

type observer struct{ ends []replay.Decision }

func (o *observer) Start(c context.Context, _ replay.Scope, _, _ string, _ replay.TraceLink) (context.Context, func(replay.Decision)) {
	return c, func(d replay.Decision) { o.ends = append(o.ends, d) }
}
func TestOneEngineComparisonAndErrorPaths(t *testing.T) {
	for _, mode := range []string{"match", "different", "incomplete", "execution_error", "unconfirmed_stop", "unrecorded_effect", "blocked_effect", "status", "invalid_output"} {
		t.Run(mode, func(t *testing.T) {
			r, d := fixture(t)
			s := &sandbox{stopped: true, observation: replay.Observation{Complete: true, Status: 200, Output: json.RawMessage(`{"value":2,"secret":"candidate-secret"}`)}}
			o := &observer{}
			switch mode {
			case "different":
				s.observation.Output = json.RawMessage(`{"value":3}`)
			case "incomplete":
				s.observation.Complete = false
			case "execution_error":
				s.err = errors.New("private failure")
			case "unconfirmed_stop":
				s.stopped = false
			case "unrecorded_effect":
				s.observation.Effects = []string{"network_write"}
			case "status":
				s.observation.Status = 500
			case "invalid_output":
				s.observation.Output = json.RawMessage(`{"value":1,"value":2}`)
			case "blocked_effect":
				f, _ := d.Fields()
				f.Records[0].Fixtures = []replay.Fixture{{ID: "delete", Effect: replay.Destructive, Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`)}}
				d, _ = replay.NewDataset(f.Scope, f.ID, f.CreatedAt, f.ExpiresAt, f.Records, r)
			}
			e, _ := replay.NewEngine(s, r, o)
			result, err := e.Run(t.Context(), d, "run")
			want := replay.Unknown
			if mode == "match" {
				want = replay.Match
			}
			if mode == "different" || mode == "status" {
				want = replay.Different
			}
			if result.Decision != want {
				t.Fatal(result, err)
			}
			if mode == "unconfirmed_stop" && !errors.Is(err, replay.ErrUnsafe) {
				t.Fatal(err)
			}
			if mode == "blocked_effect" && s.calls != 0 {
				t.Fatal("destructive effect executed")
			}
			if len(o.ends) != 1 {
				t.Fatal("unclosed correlation")
			}
		})
	}
}
func TestWriteFixturesAndEffectOrdering(t *testing.T) {
	r, d := fixture(t)
	f, _ := d.Fields()
	record := f.Records[0]
	record.Fixtures = []replay.Fixture{{ID: "write", Effect: replay.Write, Input: json.RawMessage(`{"value":1}`), Output: json.RawMessage(`{"value":2}`)}, {ID: "read", Effect: replay.Read, Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`)}}
	record.Expected.Effects = []string{"write", "read"}
	if (replay.SideEffectSandbox{}).Check(record) != nil {
		t.Fatal("recorded write fixture blocked")
	}
	d, err := replay.NewDataset(f.Scope, f.ID, f.CreatedAt, f.ExpiresAt, []replay.Record{record}, r)
	if err != nil {
		t.Fatal(err)
	}
	s := &sandbox{stopped: true, observation: record.Expected}
	s.observation.Effects = []string{"read", "write"}
	e, _ := replay.NewEngine(s, r, &observer{})
	result, err := e.Run(t.Context(), d, "run")
	if err != nil || result.Decision != replay.Different {
		t.Fatal(result, err)
	}
}
func FuzzDataset(f *testing.F) {
	r, d := fixture(f)
	raw, _ := d.MarshalJSON()
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		d, err := replay.DecodeDataset(b, r)
		if err == nil {
			raw, _ := d.MarshalJSON()
			other, err := replay.DecodeDataset(raw, r)
			if err != nil || other.Digest() != d.Digest() {
				t.Fatal("unstable dataset")
			}
		}
	})
}
