//go:build unix

package client_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// ownerOnly is whether the file or directory at path is its owner's alone:
// mode 0600 for a file, 0700 for a directory.
func ownerOnly(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	want := os.FileMode(0o600)
	if fi.IsDir() {
		want = 0o700
	}
	if m := fi.Mode().Perm(); m != want {
		return fmt.Errorf("mode %v, want %v", m, want)
	}
	return nil
}

// widen opens dir to others: mode 0755.
func widen(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // opened to others on purpose
		t.Fatal(err)
	}
}

// openFile gives others read.
func openFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // open on purpose
		t.Fatal(err)
	}
}

// runFix runs the line a refusal gives, as a person's shell would.
func runFix(t *testing.T, line string) {
	t.Helper()
	if out, err := exec.Command("sh", "-c", line).CombinedOutput(); err != nil { //nolint:gosec // the client's own words, of the test's own file
		t.Fatalf("%s: %v: %s", line, err, out)
	}
}

// fixWord is what a refusal of an open file names.
const fixWord = "chmod"
