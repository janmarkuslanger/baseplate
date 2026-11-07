//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package build

import "os"

// tryLock has no file lock on this platform: builds are only serialized
// within one Builder (Builder.mu), not across processes.
func tryLock(*os.File) (bool, error) { return true, nil }

func unlockFile(*os.File) {}
