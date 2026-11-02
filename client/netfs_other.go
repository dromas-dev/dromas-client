//go:build !linux && !darwin

package client

// filesystemOf says nothing where this client asks the system nothing.
func filesystemOf(string) (string, error) { return "", nil }
