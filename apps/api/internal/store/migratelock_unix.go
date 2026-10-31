//go:build !windows

package store

import (
	"errors"
	"os"
	"syscall"
)

// tryFlock attempts a non-blocking exclusive lock. Non-blocking because the caller owns the retry loop: a
// blocking flock cannot be interrupted, so a stuck migrator would pin the process until it was killed, with
// no timeout and no log line saying why.
func tryFlock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	// EWOULDBLOCK *is* EAGAIN on Linux (both are 11), so a case list naming both does not compile — Go
	// rejects the duplicate constant, which is a pleasant way to be told the two names mean one thing.
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return false, err
	}
}
