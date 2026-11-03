//go:build linux

package client

import "testing"

// Each network filesystem has its statfs(2) f_type, and a local one has none in
// the table. FUSE's says nothing of what is behind it.
func TestLinuxsNetworkFilesystemsAreByTheirMagicNumbers(t *testing.T) {
	t.Parallel()
	for magic, name := range map[uint32]string{
		0x6969:     "nfs",
		0x517B:     "smb",
		0xFE534D42: "smb2",
		0xFF534D42: "cifs",
		0x5346414F: "afs",
		0x6B414653: "kafs",
		0x01021997: "9p",
		0x73757245: "coda",
		0x564C:     "ncp",
		0x19830326: "beegfs",
		0xAAD7AAEA: "panfs",
		0xBEEFDEAD: "snfs",
		0x00C36400: "ceph",
		0x47504653: "gpfs",
		0x0BD00BD0: "lustre",
	} {
		if got := linuxNetworkFilesystems[magic]; got != name {
			t.Errorf("%#x: %q, want %q", magic, got, name)
		}
	}
	for name, magic := range map[string]uint32{"ext4": 0xEF53, "xfs": 0x58465342, "btrfs": 0x9123683E, "tmpfs": 0x01021994, "overlayfs": 0x794C7630, "fuse": 0x65735546} {
		if got := linuxNetworkFilesystems[magic]; got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}
