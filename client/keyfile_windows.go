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
	// owner names an owner who is none of the three, and can change the list
	// whatever it says.
	owner string
	// others is each allowing entry for anyone else: a volume that keeps no
	// access list has one, for everyone, with every right.
	others []accessEntry
}

type accessEntry struct {
	trustee string
	rights  windows.ACCESS_MASK
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
		a.others = []accessEntry{{"everyone", ^windows.ACCESS_MASK(0)}}
		return a, nil
	}
	if err != nil {
		return a, err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return a, err
	}
	switch ok, err := trustedOwner(owner); {
	case err != nil:
		return a, err
	case !ok && owner == nil:
		a.owner = "no one"
	case !ok:
		a.owner = accountName(owner)
	}
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
				a.others = append(a.others, accessEntry{accountName(sid), ace.Mask})
			}
		default:
			// A kind whose trustee this client cannot read, or that it does not
			// know: it may allow anyone anything.
			a.others = append(a.others, accessEntry{fmt.Sprintf("an entry of type %#x", ace.Header.AceType), ^windows.ACCESS_MASK(0)})
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
		return keyRefusal(path, "is accessible by "+a.others[0].trustee)
	}
	if a.owner != "" {
		return keyRefusal(path, "is owned by "+a.owner)
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
			return fmt.Errorf("%s is writable by %s: give it an access list others cannot write", path, e.trustee)
		}
	}
	if a.owner != "" {
		return fmt.Errorf("%s is owned by %s: take its ownership (icacls /setowner)", path, a.owner)
	}
	return nil
}

// newKeyTemp is an empty file in dir for a private key, whose protected access
// list (ownersAlone) it has from its making: a file inherits its directory's
// otherwise, and a list set once it exists leaves whoever opened it before then
// their handle. The list is read back, as private reads a directory's: a volume
// that keeps none takes the call and keeps nothing, and no key goes there.
func newKeyTemp(dir string) (*os.File, error) {
	want, err := ownersAlone("")
	if err != nil {
		return nil, err
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: want}
	sa.Length = uint32(unsafe.Sizeof(sa))
	for range 10000 {
		var random [8]byte
		_, _ = rand.Read(random[:]) // never fails (crypto/rand)
		name := filepath.Join(dir, ".tmp-"+hex.EncodeToString(random[:]))
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "create", Path: name, Err: err}
		}
		f := os.NewFile(uintptr(h), name)
		ok, err := hasDACL(name, want)
		if err == nil && !ok {
			err = errNoAccessList
		}
		if err != nil {
			f.Close()       //nolint:errcheck,gosec // the check's error wins
			os.Remove(name) //nolint:errcheck,gosec // as above
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		return f, nil
	}
	return nil, fmt.Errorf("%s: no free name for a new key file", dir)
}
