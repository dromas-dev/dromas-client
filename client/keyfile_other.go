//go:build !unix && !windows

package client

const keyFix = "Keep it where only its owner can read it."

// checkKeyFile takes any key file where the system has neither Unix modes nor
// Windows' access lists to read.
func checkKeyFile(string) error { return nil }

// CheckConfigFile takes any config file where the system has neither Unix
// modes nor Windows' access lists to read.
func CheckConfigFile(string) error { return nil }

func protectKeyFile(string) error { return nil }
