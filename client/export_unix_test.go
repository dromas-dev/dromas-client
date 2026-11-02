//go:build unix

package client

// CheckConfigFileAs is CheckConfigFile for a user of this uid.
func CheckConfigFileAs(path string, uid int) error { return checkConfigFile(path, uid) }
