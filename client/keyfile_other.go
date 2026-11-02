//go:build !unix && !windows

package client

import "os"

const keyFix = "Keep it where only its owner can read it."

// checkKeyFile takes any key file where the system has neither Unix modes nor
// Windows' access lists to read.
func checkKeyFile(string) error { return nil }

// CheckConfigFile takes any config file where the system has neither Unix
// modes nor Windows' access lists to read.
func CheckConfigFile(string) error { return nil }

// NewPrivateTemp is a new empty file in dir, to rename into place.
func NewPrivateTemp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".tmp-*") }

// newKeyTemp is NewPrivateTemp for a private key.
func newKeyTemp(dir string) (*os.File, error) { return NewPrivateTemp(dir) }

// openPrivate opens name, made where it is not there.
func openPrivate(name string, flag int) (*os.File, error) {
	return os.OpenFile(name, flag|os.O_CREATE, 0o600) //nolint:gosec // a file of the mailbox's own
}
