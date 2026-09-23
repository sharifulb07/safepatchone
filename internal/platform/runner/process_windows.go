//go:build windows

package runner

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"safepatch/internal/verification"
)

const sourceOpenFlags = 0

func retryCleanup(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)) || errors.Is(err, syscall.Errno(5))
}

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	createJob      = kernel32.NewProc("CreateJobObjectW")
	setJob         = kernel32.NewProc("SetInformationJobObject")
	assignJob      = kernel32.NewProc("AssignProcessToJobObject")
	terminateJob   = kernel32.NewProc("TerminateJobObject")
	queryJob       = kernel32.NewProc("QueryInformationJobObject")
	threadSnapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	threadFirst    = kernel32.NewProc("Thread32First")
	threadNext     = kernel32.NewProc("Thread32Next")
	openThread     = kernel32.NewProc("OpenThread")
	resumeThread   = kernel32.NewProc("ResumeThread")
)

type jobBasicLimits struct {
	ProcessTime, JobTime                 int64
	Flags                                uint32
	MinimumWorkingSet, MaximumWorkingSet uintptr
	ActiveProcesses                      uint32
	Affinity                             uintptr
	Priority, Scheduling                 uint32
}
type jobExtendedLimits struct {
	Basic                                                      jobBasicLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}
type threadEntry struct {
	Size, Usage, ID, Owner      uint32
	BasePriority, DeltaPriority int32
	Flags                       uint32
}

type jobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime           int64
	PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}
type processTree struct {
	job    syscall.Handle
	killed bool
}

func prepareTree(cmd *exec.Cmd) (*processTree, error) {
	handle, _, err := createJob.Call(0, 0)
	if handle == 0 {
		return nil, err
	}
	p := &processTree{job: syscall.Handle(handle)}
	limits := jobExtendedLimits{}
	limits.Basic.Flags = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE; no breakaway.
	ok, _, err := setJob.Call(handle, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		p.close()
		return nil, err
	}
	// Suspended creation closes the spawn/assignment race. No child executes
	// until it belongs to the job. Assignment failures fail closed.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x4 | 0x200 | 0x08000000, HideWindow: true}
	return p, nil
}

func (p *processTree) attach(pid int) error {
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(pid)) // SET_QUOTA | TERMINATE
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	ok, _, err := assignJob.Call(uintptr(p.job), uintptr(process))
	if ok == 0 {
		return err
	}
	snapshot, _, err := threadSnapshot.Call(4, 0) // TH32CS_SNAPTHREAD
	if snapshot == ^uintptr(0) {
		return err
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))
	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, _ = threadFirst.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.Owner == uint32(pid) {
			thread, _, err := openThread.Call(2, 0, uintptr(entry.ID)) // THREAD_SUSPEND_RESUME
			if thread == 0 {
				return err
			}
			count, _, err := resumeThread.Call(thread)
			syscall.CloseHandle(syscall.Handle(thread))
			if uint32(count) == ^uint32(0) {
				return err
			}
			return nil
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		ok, _, _ = threadNext.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	return fmt.Errorf("suspended process thread unavailable")
}

func (p *processTree) kill() error {
	if p.job == 0 || p.killed {
		return nil
	}
	ok, _, err := terminateJob.Call(uintptr(p.job), 1)
	if ok == 0 {
		return err
	}
	// TerminateJobObject initiates termination asynchronously. Wait for every
	// descendant to exit before its workspace can be removed or reused.
	deadline := time.Now().Add(cleanupTimeout)
	for {
		var accounting jobAccounting
		ok, _, err := queryJob.Call(uintptr(p.job), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
		if ok == 0 {
			return err
		}
		if accounting.ActiveProcesses == 0 {
			break
		}
		if time.Now().After(deadline) {
			return verification.ErrCleanupFailed
		}
		time.Sleep(5 * time.Millisecond)
	}
	p.killed = true
	return nil
}
func (p *processTree) close() {
	if p.job != 0 {
		_ = p.kill()
		syscall.CloseHandle(p.job)
		p.job = 0
	}
}
