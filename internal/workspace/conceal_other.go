//go:build !windows

package workspace

func concealLocalState(string) error {
	// Dot-prefixed directories are already conventionally hidden on Unix-like hosts.
	return nil
}
