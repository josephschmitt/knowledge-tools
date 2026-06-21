//go:build windows

package vault

import (
	"os"

	"golang.org/x/sys/windows"
)

// errWouldBlock is the sentinel "lock is held by someone else" error for Windows.
var errWouldBlock = windows.ERROR_LOCK_VIOLATION

// flockNB takes a non-blocking exclusive lock via LockFileEx. Windows isn't a supported daemon
// target (scheduling integration is out of scope), but the binary must still compile and the
// one-shot job commands should lock correctly if run here.
func flockNB(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ol,
	)
	if err == windows.ERROR_IO_PENDING || err == windows.ERROR_LOCK_VIOLATION {
		return errWouldBlock
	}
	return err
}
