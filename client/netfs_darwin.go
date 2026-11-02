//go:build darwin

package client

import "syscall"

// darwinNetworkFilesystems are statfs(2)'s f_fstypename of the network
// filesystems.
var darwinNetworkFilesystems = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true}

func filesystemOf(dir string) (string, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return "", err
	}
	b := make([]byte, 0, len(s.Fstypename))
	for _, c := range s.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c)) //nolint:gosec // an ASCII name
	}
	if name := string(b); darwinNetworkFilesystems[name] {
		return name, nil
	}
	return "", nil
}
