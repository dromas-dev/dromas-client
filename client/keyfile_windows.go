//go:build windows

package client

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const keyFix = "Give it a protected access list of its user, SYSTEM and the Administrators alone (icacls), as OpenSSH for Windows takes on a key file."

// readOnlyRights are the rights of an access list entry that write nothing. Any
// other reads as a write, so a right this list does not name refuses a file and
// never lets one through.
const readOnlyRights windows.ACCESS_MASK = windows.FILE_READ_DATA | windows.FILE_READ_EA | windows.FILE_EXECUTE |
	windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE | windows.GENERIC_READ | windows.GENERIC_EXECUTE

// An access list entry's types beyond the two x/sys names: winnt.h.
const (
	accessAllowedCallbackACE   = 0x9
	accessDeniedObjectACE      = 0x6
	accessDeniedCallbackACE    = 0xA
	accessDeniedCallbackObjACE = 0xC
)

// fileAccess is who a file's owner and access list let in besides the user running
// the client, SYSTEM and the local Administrators, as OpenSSH for Windows reads a
// key file's (contrib/win32/win32compat/w32-sshfileperm.c), entry by entry.
type fileAccess struct {
	// foreignOwner is an owner who is none of the three, and can change the list
	// whatever it says, named by owner.
	foreignOwner bool
	owner        *windows.SID
	// others is each allowing entry for anyone else: a volume that keeps no
	// access list has one, for everyone, with every right.
	others []accessEntry
}

// accessEntry is one allowing entry: its trustee, by SID, or in words where it has
// none to read.
type accessEntry struct {
	sid    *windows.SID
	words  string
	rights windows.ACCESS_MASK
	// inheritOnly is an entry that grants nothing on a directory itself, only
	// on what is made in it.
	inheritOnly bool
}

// trustee is who the entry names. An account's name is looked up only for a
// refusal: a lookup may go over the network.
func (e accessEntry) trustee() string {
	if e.sid == nil {
		return e.words
	}
	return accountName(e.sid)
}

func trusted(sid *windows.SID) (bool, error) {
	self, err := processUser()
	if err != nil {
		return false, err
	}
	return sid.Equals(self) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid), nil
}

// accountName is a trustee for a person to read.
func accountName(sid *windows.SID) string {
	if sid == nil {
		return "no one"
	}
	account, domain, _, err := sid.LookupAccount("")
	switch {
	case err != nil:
		return sid.String()
	case domain == "":
		return account
	}
	return domain + `\` + account
}

func readAccess(path string) (fileAccess, error) {
	var a fileAccess
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return a, err
	}
	dacl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || err == nil && dacl == nil {
		a.others = []accessEntry{{words: "everyone", rights: ^windows.ACCESS_MASK(0)}}
		return a, nil
	}
	if err != nil {
		return a, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return a, err
	}
	ok, err := trustedOwner(owner)
	if err != nil {
		return a, err
	}
	a.foreignOwner, a.owner = !ok, owner
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return a, err
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE, accessDeniedObjectACE, accessDeniedCallbackACE, accessDeniedCallbackObjACE:
		case windows.ACCESS_ALLOWED_ACE_TYPE, accessAllowedCallbackACE:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)) //nolint:gosec // an entry's SID starts at SidStart
			ok, err := trusted(sid)
			if err != nil {
				return a, err
			}
			if !ok {
				a.others = append(a.others, accessEntry{sid: sid, rights: ace.Mask, inheritOnly: ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0})
			}
		default:
			// A kind whose trustee this client cannot read, or that it does not
			// know: it may allow anyone anything.
			a.others = append(a.others, accessEntry{words: fmt.Sprintf("an entry of type %#x", ace.Header.AceType), rights: ^windows.ACCESS_MASK(0)})
		}
	}
	return a, nil
}

func trustedOwner(owner *windows.SID) (bool, error) {
	if owner == nil {
		return false, nil
	}
	return trusted(owner)
}

// checkKeyFile refuses a private key file anyone but its owner, SYSTEM and the
// Administrators can reach (readAccess).
func checkKeyFile(path string) error {
	a, err := readAccess(path)
	if err != nil {
		return err
	}
	if len(a.others) > 0 {
		return keyRefusal(path, "is accessible by "+a.others[0].trustee())
	}
	if a.foreignOwner {
		return fmt.Errorf("the private key %s is owned by %s: ssh would refuse it too. Take its ownership (icacls /setowner), and check who else holds a copy", path, accountName(a.owner))
	}
	return nil
}

// checkPrivateFile refuses a private file of the mailbox's that another user owns,
// or that anyone but its owner, SYSTEM and the Administrators can reach
// (readAccess; judgeFiles). It judges the owner before the list, because a file's
// owner can change its list: so the icacls line a refusal of the list gives
// leaves a file that passes. That line leaves the list to those three alone, by
// SID, which every shell and language reads alike.
func checkPrivateFile(path string) error {
	a, err := readAccess(path)
	if err != nil {
		return err
	}
	self, err := processUser()
	if err != nil {
		return err
	}
	if a.foreignOwner {
		return fmt.Errorf(`the mailbox's file %s is owned by %s, who can read and change it. If you made it, run: icacls "%s" /setowner *%s. Otherwise delete it`, path, accountName(a.owner), path, self)
	}
	if len(a.others) > 0 {
		fix := fmt.Sprintf(`icacls "%s" /inheritance:r /grant:r *%s:F *S-1-5-18:F *S-1-5-32-544:F`, path, self)
		sep := " /remove"
		for _, e := range a.others {
			if e.sid != nil {
				fix += sep + " *" + e.sid.String()
				sep = ""
			}
		}
		return fmt.Errorf("the mailbox's file %s is accessible by %s, and it is private as a key is. Run: %s", path, a.others[0].trustee(), fix)
	}
	return nil
}

// CheckConfigFile refuses a config file others can write (readAccess), and one
// another user owns, who can give themselves the write.
func CheckConfigFile(path string) error {
	a, err := readAccess(path)
	if err != nil {
		return err
	}
	for _, e := range a.others {
		if e.rights&^readOnlyRights != 0 {
			return fmt.Errorf("%s is writable by %s: give it an access list others cannot write", path, e.trustee())
		}
	}
	if a.foreignOwner {
		return fmt.Errorf("%s is owned by %s: take its ownership (icacls /setowner)", path, accountName(a.owner))
	}
	return nil
}

// checkKeptDir refuses inbox-dir or held/ that another user owns, or that anyone
// but its owner, SYSTEM and the Administrators can write (readAccess): whoever can
// write there can put in a file that reads as a delivered message (#717).
// Others may read it: each file in it is judged on its own (judgeKept). It judges
// the owner before the list, as checkPrivateFile does. The refusal of a list
// gives the icacls line that leaves it to those three alone.
func checkKeptDir(path string) error {
	a, err := readAccess(path)
	if err != nil {
		return err
	}
	self, err := processUser()
	if err != nil {
		return err
	}
	if a.foreignOwner {
		return fmt.Errorf(`the mailbox's directory %s is owned by %s, who could put a message in it that reads as delivered. If you made it, run: icacls "%s" /setowner *%s`, path, accountName(a.owner), path, self)
	}
	var writer *accessEntry
	fix := fmt.Sprintf(`icacls "%s" /inheritance:r /grant:r *%s:(OI)(CI)F *S-1-5-18:(OI)(CI)F *S-1-5-32-544:(OI)(CI)F`, path, self)
	sep := " /remove"
	for i, e := range a.others {
		if e.inheritOnly || e.rights&^readOnlyRights == 0 {
			continue
		}
		if writer == nil {
			writer = &a.others[i]
		}
		if e.sid != nil {
			fix += sep + " *" + e.sid.String()
			sep = ""
		}
	}
	if writer != nil {
		return fmt.Errorf("the mailbox's directory %s is writable by %s, who could put a message in it that reads as delivered. Run: %s", path, writer.trustee(), fix)
	}
	return nil
}

// createPrivate makes a new file with a protected access list (ownersAlone) from
// its making: a file inherits its directory's otherwise, and a list set once it
// exists leaves whoever opened it before then their handle.
func createPrivate(name string, want *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	sa := windows.SecurityAttributes{SecurityDescriptor: want}
	sa.Length = uint32(unsafe.Sizeof(sa))
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

// NewPrivateTemp is a new empty file in dir, to rename into place, its owner's
// alone from its making (createPrivate).
func NewPrivateTemp(dir string) (*os.File, error) {
	want, err := ownersAlone("")
	if err != nil {
		return nil, err
	}
	for range 10000 {
		var random [8]byte
		_, _ = rand.Read(random[:]) // never fails (crypto/rand)
		name := filepath.Join(dir, ".tmp-"+hex.EncodeToString(random[:]))
		h, err := createPrivate(name, want)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "create", Path: name, Err: err}
		}
		return os.NewFile(uintptr(h), name), nil
	}
	return nil, fmt.Errorf("%s: no free name for a new file", dir)
}

// newKeyTemp is NewPrivateTemp for a private key, whose list is read back, as
// private reads a directory's: a volume that keeps none takes the call and keeps
// nothing, and no key goes there.
func newKeyTemp(dir string) (*os.File, error) {
	f, err := NewPrivateTemp(dir)
	if err != nil {
		return nil, err
	}
	want, err := ownersAlone("")
	if err == nil {
		var ok bool
		if ok, err = hasDACL(f.Name(), want); err == nil && !ok {
			err = errNoAccessList
		}
	}
	if err != nil {
		f.Close()           //nolint:errcheck,gosec // the check's error wins
		os.Remove(f.Name()) //nolint:errcheck,gosec // as above
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	return f, nil
}

// newPrivateFile makes name, a new file with a protected access list from its
// making (createPrivate), and refuses it where anything is there (CREATE_NEW).
func newPrivateFile(name string) (*os.File, error) {
	want, err := ownersAlone("")
	if err != nil {
		return nil, err
	}
	h, err := createPrivate(name, want)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}

// openPrivate opens name, made its owner's alone where it is not there
// (createPrivate).
func openPrivate(name string, flag int) (*os.File, error) {
	want, err := ownersAlone("")
	if err != nil {
		return nil, err
	}
	h, err := createPrivate(name, want)
	switch {
	case err == nil:
		_ = windows.CloseHandle(h) // opened below, as any other file
	case !errors.Is(err, windows.ERROR_FILE_EXISTS):
		return nil, &os.PathError{Op: "create", Path: name, Err: err}
	}
	return os.OpenFile(name, flag, 0) //nolint:gosec // a file of the mailbox's own
}
