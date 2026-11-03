//go:build darwin

package client

import "testing"

// Each network filesystem is by its statfs(2) f_fstypename, and a local one is
// none, as is a name that fills the field with no NUL to end it.
func TestDarwinsNetworkFilesystemsAreByTheirNames(t *testing.T) {
	t.Parallel()
	field := func(name string) (f [16]int8) {
		for i := range len(name) {
			f[i] = int8(name[i]) //nolint:gosec // an ASCII name
		}
		return f
	}
	for _, name := range []string{"nfs", "smbfs", "afpfs", "webdav", "cifs", "acfs"} {
		if got := networkFilesystemNamed(field(name)); got != name {
			t.Errorf("%s: %q", name, got)
		}
	}
	for _, name := range []string{"apfs", "hfs", "msdos", "exfat", "devfs", "nfs-but-longer16"} {
		if got := networkFilesystemNamed(field(name)); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}
