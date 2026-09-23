//go:build linux || darwin

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

const sourceOpenFlags = syscall.O_RDONLY | syscall.O_NONBLOCK

func retryCleanup(error) bool { return false }

type processTree struct {
	pid    int
	killed bool
}

func prepareTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &processTree{}, nil
}
func (p *processTree) attach(pid int) error { p.pid = pid; return nil }
func (p *processTree) kill() error {
	if p.pid == 0 || p.killed {
		return nil
	}
	err := syscall.Kill(-p.pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		p.killed = true
		return nil
	}
	return err
}
func (p *processTree) close() { _ = p.kill() }
