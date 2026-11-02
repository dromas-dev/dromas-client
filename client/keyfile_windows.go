//go:build windows

package client

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

const keyFix = "Give it a protected access list of its user, SYSTEM and the Administrators alone (icacls), as OpenSSH for Windows takes on a key file."

// readOnlyRights are an access list entry's rights, in the text form, that
// write nothing.
var readOnlyRights = map[string]bool{"FR": true, "GR": true, "FRFX": true, "GRGX": true, "FX": true, "GX": true, "0x1200a9": true, "0x120089": true, "0x1200a0": true}

// foreign is each allowing entry of path's access list for anyone but the user
// running the client, SYSTEM and the local Administrators, as OpenSSH for
// Windows reads a key file's (contrib/win32/win32compat/w32-sshfileperm.c):
// its rights by its trustee. A volume that keeps no access list has one entry,
// for everyone: "D:NO_ACCESS_CONTROL".
func foreign(path string) (map[string]string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the user running the client: %w", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	// The text form writes some accounts by an alias: the user running the
	// client is read back through it as its own entries are.
	self, err := windows.SecurityDescriptorFromString("D:(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	selfAce := strings.Split(strings.TrimSuffix(aces(self.String()), ")"), ";")
	sddl := sd.String()
	out := map[string]string{}
	if strings.Contains(sddl, "NO_ACCESS_CONTROL") {
		out["everyone"] = "FA"
		return out, nil
	}
	for _, ace := range strings.Split(aces(sddl), "(") {
		f := strings.Split(strings.TrimSuffix(ace, ")"), ";")
		if len(f) != 6 || !strings.HasPrefix(f[0], "A") {
			continue
		}
		switch t := f[5]; t {
		case selfAce[len(selfAce)-1], "SY", "BA":
		default:
			out[t] = f[2]
		}
	}
	return out, nil
}

// checkKeyFile refuses a private key file anyone but its owner, SYSTEM and the
// Administrators can reach (foreign).
func checkKeyFile(path string) error {
	others, err := foreign(path)
	if err != nil {
		return err
	}
	for t := range others {
		return keyRefusal(path, "is accessible by "+t)
	}
	return nil
}

// CheckConfigFile refuses a config file others can write (foreign).
func CheckConfigFile(path string) error {
	others, err := foreign(path)
	if err != nil {
		return err
	}
	for t, rights := range others {
		if !readOnlyRights[rights] {
			return fmt.Errorf("%s is writable by %s: give it an access list others cannot write", path, t)
		}
	}
	return nil
}

// protectKeyFile gives a new key file a protected access list of its user,
// SYSTEM and the Administrators, as private gives a directory, since a new
// file inherits its directory's.
func protectKeyFile(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("the user running the client: %w", err)
	}
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)")
	if err != nil {
		return err
	}
	dacl, _, err := want.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
