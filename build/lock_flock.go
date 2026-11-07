//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package build

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive advisory lock on f without blocking. The
// kernel releases it when the process exits, so a crashed build never
// leaves a stale lock.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlockFile(f *os.File) { syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
