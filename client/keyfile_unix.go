//go:build unix

package client

import (
	"fmt"
	"os"
	"syscall"
)

const keyFix = "chmod 600 it, or set the mode of the mount it comes from: a Kubernetes volume's defaultMode: 0400, a Compose secret's mode: 0400."

// checkKeyFile refuses a private key file others can reach, as ssh(1) does
// ("accessible by others"): any mode bit for its group or for everyone. It
// judges the identity, a key its owner may put in place. As OpenSSH's
// sshkey_perm_ok does, it leaves a key another user owns to that user. The
// keys and the ticket the client writes are private files (checkPrivateFile).
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

// checkPrivateFile refuses a private file of the mailbox's that others can reach
// or another user owns (judgeFiles), and so each prekey file, working key and
// TLS ticket the client writes in the rotating keys' directory.
func checkPrivateFile(path string) error { return checkPrivateFileAs(path, os.Getuid()) }

func checkPrivateFileAs(path string, me int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode().Perm(); m&0o077 != 0 {
		return fmt.Errorf("the mailbox's file %s is accessible by others (mode %04o), and it is private as a key is. Run: chmod 600 %q", path, m, path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int64(st.Uid) != int64(me) {
		return fmt.Errorf("the mailbox's file %s is owned by another user (uid %d), who can read and change it: chown it to yourself if you made it, and otherwise delete it", path, st.Uid)
	}
	return nil
}

// checkKeptDir refuses inbox-dir or held/ where others can write it or another
// user owns it, as sshd's StrictModes refuses a directory on the way to a key:
// whoever can write there can put in a file that reads as a delivered message
// (#717). Others may read it: each file in it is judged on its own (judgeKept).
func checkKeptDir(path string) error { return checkKeptDirAs(path, os.Getuid()) }

func checkKeptDirAs(path string, me int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode().Perm(); m&0o022 != 0 {
		return fmt.Errorf("the mailbox's directory %s is writable by others (mode %04o), who could put a message in it that reads as delivered. Run: chmod go-w %q", path, m, path)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int64(st.Uid) != int64(me) {
		return fmt.Errorf("the mailbox's directory %s is owned by another user (uid %d), who could put a message in it that reads as delivered: chown it to yourself if you made it", path, st.Uid)
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
