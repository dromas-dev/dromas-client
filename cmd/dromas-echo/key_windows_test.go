//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// The echo's copy of its identity has a protected access list from its making,
// so it takes nothing from the directory it is in.
func TestTheIdentitysCopyIsItsOwnersAlone(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "identity")
	if err := writeKey(dir, name, []byte("key")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != "key" {
		t.Fatalf("the copy: %q %v", got, err)
	}
	sd, err := windows.GetNamedSecurityInfo(name, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the copy's access list is not protected: %s", sd)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("%d entries in the directory, want the copy alone", len(es))
	}
}
