//go:build unix

package client

// CheckConfigFileAs is CheckConfigFile for a user of this uid.
func CheckConfigFileAs(path string, uid int) error { return checkConfigFile(path, uid) }

// CheckPrivateFileAs is checkPrivateFile, for a test that says which user it is.
func CheckPrivateFileAs(path string, me int) error { return checkPrivateFileAs(path, me) }

// CheckKeptDirAs is checkKeptDir, for a test that says which user it is.
func CheckKeptDirAs(path string, me int) error { return checkKeptDirAs(path, me) }

// CheckKeyFileAs is checkKeyFile as the user me runs it.
func CheckKeyFileAs(path string, me int) error { return checkKeyFileAs(path, me) }
