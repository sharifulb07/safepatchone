//go:build windows

package runner

import (
	"syscall"
	"testing"
	"time"
)

func TestWorkspaceCleanupWaitsForWindowsSharingHandle(t *testing.T) {
	_, request, base := localFixture(t)
	w, err := snapshot(t.Context(), base, request.SourceDirectory, request.Limits)
	if err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(w.directory)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		_ = w.close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); time.Sleep(50 * time.Millisecond); syscall.CloseHandle(handle) }()
	err = w.close()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	assertEmpty(t, base)
}
