package build

import (
	"errors"

	"golang.org/x/sys/unix"
)

// exchange swaps the directories a and b in one step (renameat2 with
// RENAME_EXCHANGE). Kernels and file systems without it, and a missing b,
// give errNoExchange.
func exchange(a, b string) error {
	err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOENT) {
		return errNoExchange
	}
	return err
}
