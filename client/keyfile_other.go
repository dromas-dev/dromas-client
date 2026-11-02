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

// newKeyTemp is an empty file in dir for a private key.
func newKeyTemp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".tmp-*") }
