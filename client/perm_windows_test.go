//go:build windows

package client_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/dromas-dev/dromas-client/client"

	"golang.org/x/sys/windows"
)

// ownerOnly is whether the file or directory at path is its owner's alone
// as Windows has it (private): its access list allows nothing to anyone but
// the user running the test, SYSTEM and the local Administrators.
func ownerOnly(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("an empty access list, which allows everyone everything")
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // an ACE's SID starts at SidStart
		if !windows.EqualSid(sid, user.User.Sid) && !windows.EqualSid(sid, system) && !windows.EqualSid(sid, admins) {
			return fmt.Errorf("its access list allows %s", sid)
		}
	}
	return nil
}

// widen opens dir to others as a directory under C:\ is: a protected access
// list that also gives the local Users, inherited by what is made in it.
func widen(t *testing.T, dir string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// setList gives path the access list of this text form, protected, with the user
// running the test, SYSTEM and the Administrators in it.
func setList(t *testing.T, path, entries string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)" + entries)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// A file's access list is judged entry by entry, as OpenSSH for Windows judges a
// key file: any entry that lets another in refuses a key, and any that lets another
// write refuses a config, whatever right it names and however many it has.
func TestAnAccessListIsJudgedEntryByEntry(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, entries string
		key, config   bool // taken
	}{
		{"the three alone", "", true, true},
		{"a deny for another", "(D;;FA;;;WD)", true, true},
		{"read for the users", "(A;;FR;;;BU)", false, true},
		{"read and run for the users", "(A;;0x1200a9;;;BU)", false, true},
		{"read-data alone", "(A;;0x1;;;BU)", false, true},
		{"read-attributes and synchronize", "(A;;0x100080;;;BU)", false, true},
		{"read-control alone", "(A;;0x20000;;;BU)", false, true},
		{"write for the users", "(A;;FW;;;BU)", false, false},
		{"all for everyone", "(A;;FA;;;WD)", false, false},
		{"write-data alone", "(A;;0x2;;;BU)", false, false},
		{"append alone", "(A;;0x4;;;BU)", false, false},
		{"write-attributes alone", "(A;;0x100;;;BU)", false, false},
		{"delete alone", "(A;;0x10000;;;BU)", false, false},
		{"write the list alone", "(A;;0x40000;;;BU)", false, false},
		{"take ownership alone", "(A;;0x80000;;;BU)", false, false},
		{"a write and a read for one trustee", "(A;;FA;;;BU)(A;;FR;;;BU)", false, false},
		{"a read and a write for one trustee", "(A;;FR;;;BU)(A;;FA;;;BU)", false, false},
		{"a conditional allow", "(XA;;FA;;;WD;(Member_of {SID(WD)}))", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), "f")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			setList(t, file, c.entries)
			if err := client.CheckKeyFile(file); (err == nil) != c.key {
				t.Errorf("as a key: %v", err)
			}
			if err := client.CheckConfigFile(file); (err == nil) != c.config {
				t.Errorf("as a config: %v", err)
			}
		})
	}
}

// A file another user owns is refused: they can give themselves the list.
func TestAFileAnotherUserOwnsIsRefused(t *testing.T) {
	t.Parallel()
	if !windows.GetCurrentProcessToken().IsElevated() {
		return // only an administrator can give a file away
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setList(t, file, "")
	if out, err := exec.Command("icacls", file, "/setowner", "*S-1-5-32-545").CombinedOutput(); err != nil { //nolint:gosec // the test's own file
		t.Fatalf("%v: %s", err, out)
	}
	if err := client.CheckKeyFile(file); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("as a key: %v", err)
	}
	if err := client.CheckConfigFile(file); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("as a config: %v", err)
	}
}

// A new key's file is its owner's alone before it holds a byte, in a directory that
// gives others access: a list set later leaves whoever opened it their handle.
func TestAKeyFileIsItsOwnersAloneFromItsMaking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	widen(t, dir)
	plain, err := os.CreateTemp(dir, "plain-*")
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close() //nolint:errcheck // the test's own
	if err := ownerOnly(plain.Name()); err == nil {
		t.Fatal("this directory gives a new file nothing to protect it from")
	}
	f, err := client.NewKeyTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // the test's own
	if err := ownerOnly(f.Name()); err != nil {
		t.Error(err)
	}
	if err := client.CheckKeyFile(f.Name()); err != nil {
		t.Error(err)
	}
	if _, err := f.WriteString("written through its one handle"); err != nil {
		t.Error(err)
	}
}

// A long path takes a new key's file as it takes every other.
func TestAKeyFileAtALongPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for range 6 {
		dir = filepath.Join(dir, strings.Repeat("x", 50))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := client.NewKeyTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // the test's own
	if err := ownerOnly(f.Name()); err != nil {
		t.Error(err)
	}
}
