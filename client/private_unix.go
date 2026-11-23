//go:build unix

package client

import "os"

// private sets a directory of the mailbox to mode 0700, where
// MkdirAll leaves an existing directory's mode as it was. Every file in it is
// written 0600 (writeFile). An access list the directory passes down, as a
// macOS directory can, is the owner's: a file written here carries it, and
// grants what it grants beside the mode.
func private(dir string) error {
	return os.Chmod(dir, 0o700) //nolint:gosec // a directory: 0700 is owner-only
}
