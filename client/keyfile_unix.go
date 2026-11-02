//go:build unix

package client

import (
	"fmt"
	"os"
	"syscall"
)

const keyFix = "chmod 600 it, or set the mode of the mount it comes from: a Kubernetes volume's defaultMode: 0400, a Compose secret's mode: 0400."

// checkKeyFile refuses a private key file others can reach, as ssh(1) does
// ("accessible by others"): any mode bit for its group or for everyone.
func checkKeyFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		return keyRefusal(path, fmt.Sprintf("is accessible by others (mode %04o)", m))
	}
	return nil
}

// CheckConfigFile refuses a config file others can write, as ssh_config(5)
// does: whoever can write it says where the mailbox's keys are and which
// server it calls. Its owner can, whatever its mode says, so one who is neither
// this user nor root is refused too, as ssh does.
func CheckConfigFile(path string) error { return checkConfigFile(path, os.Getuid()) }

func checkConfigFile(path string, me int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode().Perm(); m&0o022 != 0 {
		return fmt.Errorf("%s is writable by others (mode %04o): chmod go-w it", path, m)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int64(st.Uid) != int64(me) {
		return fmt.Errorf("%s is owned by another user (uid %d): chown it to yourself", path, st.Uid)
	}
	return nil
}

// NewPrivateTemp is a new empty file in dir, to rename into place, its owner's
// alone from its making: mode 0600, which os.CreateTemp gives every file.
func NewPrivateTemp(dir string) (*os.File, error) { return os.CreateTemp(dir, ".tmp-*") }

// newKeyTemp is NewPrivateTemp for a private key.
func newKeyTemp(dir string) (*os.File, error) { return NewPrivateTemp(dir) }

// openPrivate opens name, made its owner's alone where it is not there.
func openPrivate(name string, flag int) (*os.File, error) {
	return os.OpenFile(name, flag|os.O_CREATE, 0o600) //nolint:gosec // a file of the mailbox's own
}
