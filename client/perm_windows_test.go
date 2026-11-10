//go:build windows

package client_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"

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
func widen(t *testing.T, dir string) { t.Helper(); widenWith(t, dir, "0x1301bf") }

// widenToRead gives the local Users read of dir alone, inherited by what is made
// in it.
func widenToRead(t *testing.T, dir string) { t.Helper(); widenWith(t, dir, "0x1200a9") }

// widenWith is widen, with the local Users given these rights.
func widenWith(t *testing.T, dir, rights string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;" + rights + ";;;BU)")
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

// accessLists is what a file's access list may say beside the three, and what is
// taken of each as a key and as a config.
var accessLists = []struct {
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
}

// A file's access list is judged entry by entry, as OpenSSH for Windows judges a
// key file: any entry that lets another in refuses a key, and any that lets another
// write refuses a config, whatever right it names and however many it has.
func TestAnAccessListIsJudgedEntryByEntry(t *testing.T) {
	t.Parallel()
	for _, c := range accessLists {
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
			if err := client.CheckPrivateFile(file); (err == nil) != c.key {
				t.Errorf("as a mailbox's private file: %v", err)
			}
			if err := client.CheckConfigFile(file); (err == nil) != c.config {
				t.Errorf("as a config: %v", err)
			}
		})
	}
}

// setUnrenderable gives path the three and an entry allowing everyone all, beside a
// callback entry whose data the list's text form cannot write, so that form of the
// whole list is empty. The list is built from its bytes: no text form makes it.
func setUnrenderable(t *testing.T, path string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	// An access list's layout, and an entry's: winnt.h's ACL and ACCESS_ALLOWED_CALLBACK_ACE.
	const header, aceHeader = 8, 8
	size := int(binary.LittleEndian.Uint16(unsafe.Slice((*byte)(unsafe.Pointer(dacl)), header)[2:])) //nolint:gosec // an access list starts with its header
	entry := make([]byte, aceHeader, aceHeader+everyone.Len()+16)
	entry[0] = 0x9                                                                            // ACCESS_ALLOWED_CALLBACK_ACE_TYPE
	binary.LittleEndian.PutUint32(entry[4:], 0x1f01ff)                                        // FA
	entry = append(entry, unsafe.Slice((*byte)(unsafe.Pointer(everyone)), everyone.Len())...) //nolint:gosec // a SID's own length
	entry = append(entry, []byte("artx\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff")...)
	binary.LittleEndian.PutUint16(entry[2:], uint16(len(entry))) //nolint:gosec // a few dozen bytes
	words := make([]uint32, (size+len(entry))/4+1)
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*4) //nolint:gosec // the words' own bytes, aligned as a list must be
	copy(raw, unsafe.Slice((*byte)(unsafe.Pointer(dacl)), size))          //nolint:gosec // the list's own size
	copy(raw[size:], entry)
	raw[0] = 4                                                      // ACL_REVISION_DS, which a callback entry takes
	binary.LittleEndian.PutUint16(raw[2:], uint16(size+len(entry))) //nolint:gosec // a few dozen bytes
	binary.LittleEndian.PutUint16(raw[4:], binary.LittleEndian.Uint16(raw[4:])+1)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, (*windows.ACL)(unsafe.Pointer(&raw[0])), nil); err != nil { //nolint:gosec // a list, as built above
		t.Fatal(err)
	}
	got, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if s := got.String(); s != "" {
		t.Fatalf("its text form writes this list, so this holds nothing: %s", s)
	}
}

// A list the text form cannot write is read entry by entry as any other, and its
// entry for everyone refuses the file.
func TestAListTheTextFormCannotWriteIsRefused(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setUnrenderable(t, file)
	if err := client.CheckKeyFile(file); err == nil || !strings.Contains(err.Error(), "accessible by") {
		t.Errorf("as a key: %v", err)
	}
	if err := client.CheckConfigFile(file); err == nil || !strings.Contains(err.Error(), "writable by") {
		t.Errorf("as a config: %v", err)
	}
}

// A mailbox is made, rekeyed and opened under the account running the test, each
// key's list its three alone, one entry for each: SYSTEM is one trustee of the three.
func TestAMailboxIsMadeUnderThisAccount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	dir := filepath.Join(t.TempDir(), "made")
	a, err := client.Init(t.Context(), dir, e.opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Open(dir, e.opts()); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := 3
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		want = 2
	}
	sd, err := windows.GetNamedSecurityInfo(filepath.Join(dir, "identity"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if got := sd.String(); strings.Count(got, "(A;") != want {
		t.Errorf("the identity's list has not %d entries: %s", want, got)
	}
	if err := ownerOnly(filepath.Join(dir, "identity")); err != nil {
		t.Error(err)
	}
}

// A private key file others can reach is refused where it is read, whether it is
// the identity or a prekey, and taken once its list is the three's. The refusal
// names who reaches it, and the fix.
func TestAPrivateKeyOthersCanReachIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	identity := filepath.Join(b.Dir(), "identity")
	setList(t, identity, "(A;;FR;;;BU)")
	if _, err := client.Open(b.Dir(), e.opts()); err == nil || !strings.Contains(err.Error(), "accessible by") || !strings.Contains(err.Error(), "icacls") {
		t.Errorf("an identity the users can read: %v", err)
	}
	setList(t, identity, "")
	if _, err := client.Open(b.Dir(), e.opts()); err != nil {
		t.Errorf("an identity of the three: %v", err)
	}
	keys, err := filepath.Glob(filepath.Join(b.Dir(), "keys", "prekeys", "*.agekey"))
	if err != nil || len(keys) == 0 {
		t.Fatal(keys, err)
	}
	a := e.heldMailbox()
	send(t, a, b, "waits")
	setList(t, keys[0], "(A;;FR;;;BU)")
	if _, err := b.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), "accessible by") {
		t.Errorf("a prekey the users can read: %v", err)
	}
}

// A working key others can reach is refused where it signs: a send, whose pass and
// message it signs.
func TestAWorkingKeyOthersCanReachIsRefused(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	a, b := e.mailbox(), e.mailbox()
	if !certified(t, a) {
		t.Fatal("not under a certificate")
	}
	keys, err := filepath.Glob(filepath.Join(a.Dir(), "keys", "work", "*"))
	if err != nil || len(keys) == 0 {
		t.Fatal(keys, err)
	}
	for _, k := range keys {
		if !strings.Contains(filepath.Base(k), ".") {
			setList(t, k, "(A;;FR;;;BU)")
		}
	}
	if _, err := a.Send(t.Context(), b.Address(), "x"); err == nil || !strings.Contains(err.Error(), "accessible by") {
		t.Errorf("send: %v", err)
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
	if err := client.CheckPrivateFile(file); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("as a mailbox's private file: %v", err)
	}
	if err := client.CheckConfigFile(file); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Errorf("as a config: %v", err)
	}
}

// A private file and a kept directory, each both open to others and owned by
// another user, are refused for their owner, who can change the list whatever
// it says: the icacls line that sets the list is no fix while another user owns
// it.
func TestTheOwnerIsJudgedBeforeTheList(t *testing.T) {
	t.Parallel()
	if !windows.GetCurrentProcessToken().IsElevated() {
		return // only an administrator can give a file away
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, check := range map[string]func(string) error{file: client.CheckPrivateFile, dir: client.CheckKeptDir} {
		setList(t, path, "(A;;FA;;;BU)")
		if out, err := exec.Command("icacls", path, "/setowner", "*S-1-5-32-545").CombinedOutput(); err != nil { //nolint:gosec // the test's own file
			t.Fatalf("%v: %s", err, out)
		}
		if err := check(path); err == nil || !strings.Contains(err.Error(), "owned by") || strings.Contains(err.Error(), "/grant") {
			t.Errorf("%s: %v; want the refusal of its owner, with no line that sets its list", path, err)
		}
	}
}

// openFile gives the local Users read.
func openFile(t *testing.T, path string) {
	t.Helper()
	setList(t, path, "(A;;FR;;;BU)")
}

// openToWrites lets the users write in dir, as widen does.
func openToWrites(t *testing.T, dir string) { t.Helper(); widen(t, dir) }

// runFix runs the line a refusal gives, as a person's cmd.exe would.
func runFix(t *testing.T, line string) {
	t.Helper()
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: "cmd /c " + line}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", line, err, out)
	}
}

// fixWord is what a refusal of an open file names.
const fixWord = "icacls"

// A state file planted in a directory that gives others access, and the lists
// planted beside it, keep the list they inherit there, so init refuses them.
func TestAFilePlantedInAWideDirectoryIsRefusedByInit(t *testing.T) {
	t.Parallel()
	for _, f := range client.PrivateFiles() {
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			dir := t.TempDir()
			widen(t, dir)
			planted := filepath.Join(dir, f)
			if err := os.WriteFile(planted, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Init(t.Context(), dir, e.opts()); err == nil || !strings.Contains(err.Error(), planted) || !strings.Contains(err.Error(), fixWord) {
				t.Fatalf("init over a planted %s: %v", f, err)
			}
			if exists(filepath.Join(dir, "identity")) {
				t.Fatal("init went on to make the identity")
			}
		})
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

// outbox/failed/ is made as every directory of a mailbox is (makeDir), with a
// protected access list of its own, never the list outbox/ passes down.
func TestOutboxFailedHasAProtectedListOfItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	forge.FailOnce(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	if _, err := a.Send(ctx, b.Address(), "stuck"); err == nil {
		t.Fatal("the send should have failed")
	}
	e.clock.Add(protocol.TTL + time.Second)
	if _, err := a.Flush(ctx); err == nil {
		t.Fatal("the stale entry should have moved to outbox/failed/")
	}
	failed := filepath.Join(a.Dir(), "outbox", "failed")
	sd, err := windows.GetNamedSecurityInfo(failed, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("outbox/failed/ inherits its list: %s", sd)
	}
	if err := ownerOnly(failed); err != nil {
		t.Error(err)
	}
}
