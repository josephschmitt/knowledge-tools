//go:build !windows

package vault

import (
	"os"

	"golang.org/x/sys/unix"
)

// errWouldBlock is the sentinel "lock is held by someone else" error for this platform.
var errWouldBlock = unix.EWOULDBLOCK

// flockNB takes a non-blocking exclusive flock(2). flock(2) is available on both Linux and macOS,
// so this single implementation covers both — no mkdir fallback needed (vault-lib.sh only fell
// back to mkdir because macOS lacks the flock(1) *CLI*, not the syscall).
func flockNB(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
