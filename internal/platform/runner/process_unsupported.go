//go:build !linux && !darwin && !windows

package runner

import (
	"os/exec"
	"safepatch/internal/verification"
)

const sourceOpenFlags = 0

func retryCleanup(error) bool { return false }

type processTree struct{}

func prepareTree(*exec.Cmd) (*processTree, error) { return nil, verification.ErrRunnerUnavailable }
func (*processTree) attach(int) error             { return verification.ErrRunnerUnavailable }
func (*processTree) kill() error                  { return nil }
func (*processTree) close()                       {}
