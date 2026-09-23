// Package replayadapter supplies isolation and OpenTelemetry for the shared engine.
package replayadapter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"safepatch/internal/platform/runner"
	"safepatch/internal/replay"
	"safepatch/internal/verification"
)

type Sandbox struct {
	runner   verification.Runner
	template verification.ExecutionRequest
	root     string
}

// NewSandbox only accepts the existing enforced container runner. The executable
// must be preinstalled in its pinned image and read replay.json from its working
// directory. No repository/client commands, host data, environment credentials,
// live network or writable host mount are supplied.
func NewSandbox(r *runner.ContainerSandboxRunner, template verification.ExecutionRequest, root string) (*Sandbox, error) {
	if r == nil || !filepath.IsAbs(root) || template.Mode == verification.TrustedLocal || len(template.Environment) != 0 {
		return nil, replay.ErrInvalid
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, replay.ErrInvalid
	}
	template.SourceDirectory = root
	template.Mode = verification.Sandbox
	template.WorkingDirectory = "."
	if template.Validate() != nil || template.Limits.StdoutBytes > replay.MaxPayloadBytes || template.Limits.InputBytes > replay.MaxDatasetBytes {
		return nil, replay.ErrInvalid
	}
	template.Arguments = append([]string{}, template.Arguments...)
	template.Environment = map[string]string{}
	return &Sandbox{r, template, root}, nil
}
func (s *Sandbox) Execute(ctx context.Context, record replay.Record) (out replay.Execution, err error) {
	out.Stopped = true
	if s == nil || ctx == nil || (replay.SideEffectSandbox{}).Check(record) != nil {
		return out, replay.ErrDenied
	}
	dir, err := os.MkdirTemp(s.root, "replay-")
	if err != nil {
		return out, replay.ErrUnavailable
	}
	defer func() {
		if e := os.RemoveAll(dir); e != nil {
			err = replay.ErrUnavailable
		}
	}()
	// Never reveal the expected baseline to the candidate process.
	raw, err := json.Marshal(struct {
		Input    json.RawMessage  `json:"input"`
		Fixtures []replay.Fixture `json:"fixtures"`
	}{record.Input, record.Fixtures})
	if err != nil || len(raw) > replay.MaxDatasetBytes {
		return out, replay.ErrInvalid
	}
	if err = os.WriteFile(filepath.Join(dir, "replay.json"), raw, 0600); err != nil {
		return out, replay.ErrUnavailable
	}
	request := s.template
	request.SourceDirectory = dir
	request.Arguments = append([]string{}, s.template.Arguments...)
	request.Environment = map[string]string{}
	out.Stopped = false
	execution, err := s.runner.Run(ctx, request)
	if errors.Is(err, verification.ErrCleanupFailed) {
		return out, replay.ErrUnsafe
	}
	out.Stopped = true
	if err != nil || !execution.Sandboxed || execution.Status != verification.Completed || execution.ExitCode != 0 || execution.StdoutTruncated || execution.StderrTruncated {
		return out, replay.ErrUnavailable
	}
	out.Observation, err = replay.DecodeObservation(execution.Stdout)
	if err != nil {
		return out, replay.ErrUnavailable
	}
	return out, nil
}
