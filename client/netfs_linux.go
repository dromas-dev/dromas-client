//go:build linux

package client

import "syscall"

// linuxNetworkFilesystems are statfs(2)'s f_type of the network filesystems,
// from linux/magic.h, and coreutils' stat.c for those it does not hold. FUSE's
// says nothing of what is behind it, and one host's flock there holds, so it is
// not one.
var linuxNetworkFilesystems = map[uint32]string{
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
}

func filesystemOf(dir string) (string, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return "", err
	}
	return linuxNetworkFilesystems[uint32(s.Type)], nil //nolint:gosec // f_type is a 32-bit magic number, whatever the field's width
}
