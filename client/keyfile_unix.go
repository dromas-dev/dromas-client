//go:build unix

package client

import (
	"fmt"
	"os"
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
// server it calls.
func CheckConfigFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if m := fi.Mode().Perm(); m&0o022 != 0 {
		return fmt.Errorf("%s is writable by others (mode %04o): chmod go-w it", path, m)
	}
	return nil
}

// protectKeyFile has nothing to do where a new file's mode is 0600 (writeFile).
func protectKeyFile(string) error { return nil }
