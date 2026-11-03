//go:build darwin

package client

import "syscall"

// darwinNetworkFilesystems are statfs(2)'s f_fstypename of the network
// filesystems, those of systems older than this one's included.
var darwinNetworkFilesystems = map[string]bool{"nfs": true, "smbfs": true, "afpfs": true, "webdav": true, "cifs": true, "acfs": true}

func filesystemOf(dir string) (string, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return "", err
	}
	return networkFilesystemNamed(s.Fstypename), nil
}

// networkFilesystemNamed is the name in a statfs(2) f_fstypename where it names a
// network filesystem, and "" otherwise.
func networkFilesystemNamed(fstypename [16]int8) string {
	b := make([]byte, 0, len(fstypename))
	for _, c := range fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c)) //nolint:gosec // an ASCII name
	}
	if name := string(b); darwinNetworkFilesystems[name] {
		return name
	}
	return ""
}
