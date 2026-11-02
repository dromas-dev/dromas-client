//go:build windows

package client

import (
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
