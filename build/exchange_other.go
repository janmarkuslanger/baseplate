//go:build !(linux || darwin)

package build

// exchange is not available on this system; swap falls back to renameSwap.
func exchange(string, string) error { return errNoExchange }
