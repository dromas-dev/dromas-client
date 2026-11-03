//go:build !windows

package main

import (
	"os"
	"testing"
)

// openToWriters gives everyone the write of file.
func openToWriters(t *testing.T, file string) {
	t.Helper()
	if err := os.Chmod(file, 0o666); err != nil { //nolint:gosec // open on purpose
		t.Fatal(err)
	}
}
