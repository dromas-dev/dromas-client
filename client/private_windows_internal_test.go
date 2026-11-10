//go:build windows

package client

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A user that is SYSTEM is one entry: Windows keeps one, so a list naming it twice
// never reads back as it was set, and no mailbox could be made under LocalSystem.
func TestAListNamesSystemOnce(t *testing.T) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	if got := ownersAloneText(system, "OICI"); strings.Count(got, "(") != 2 || !strings.Contains(got, ";SY)") {
		t.Errorf("%s", got)
	}
	self, err := processUser()
	if err != nil {
		t.Fatal(err)
	}
	if !self.IsWellKnown(windows.WinLocalSystemSid) {
		if got := ownersAloneText(self, ""); strings.Count(got, "(") != 3 {
			t.Errorf("%s", got)
		}
	}
}

// newPrivateFile refuses a name that is a link to nothing, as its Unix twin's
// O_EXCL does, and makes nothing at the link's target (createPrivate).
func TestANewPrivateFileRefusesALinkToNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
	if os.Symlink(target, link) != nil {
		return // Windows may not let this user make one
	}
	if f, err := newPrivateFile(link); err == nil {
		_ = f.Close()
		t.Fatal("a link to nothing was taken as a new file")
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a file was made at the link's target: %v", err)
	}
}
