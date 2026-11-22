//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd) && !windows

package client

import "os"

// tryGate is noGate where a directory cannot be flocked: a system neither
// Windows nor one of gate_unix.go's.
func tryGate(dir string) (*os.File, bool, error) { return noGate(dir) }

// unflock is nothing where there is no flock: bbolt's lock there is a POSIX
// record lock, the process's own, which a started process does not copy.
func unflock(*os.File) {}
