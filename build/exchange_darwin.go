package build

import (
	"errors"

	"golang.org/x/sys/unix"
)

// exchange swaps the directories a and b in one step (renamex_np with
// RENAME_SWAP). File systems without it, and a missing b, give
// errNoExchange.
func exchange(a, b string) error {
	err := unix.RenamexNp(a, b, unix.RENAME_SWAP)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
		return errNoExchange
	}
	return err
}
