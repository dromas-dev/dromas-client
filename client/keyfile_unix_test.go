//go:build unix

package client_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// A private key file others can reach is refused, as ssh(1) refuses one, whether it
// is the identity, a prekey or a working key: each is a mode bit away from being
// read by anyone, and a Compose secret arrives 0444. The refusal names the fix.
func TestAPrivateKeyOthersCanReachIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	for _, mode := range []os.FileMode{0o640, 0o604, 0o444} {
		if err := os.Chmod(filepath.Join(b.Dir(), "identity"), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Open(b.Dir(), e.opts()); err == nil || !strings.Contains(err.Error(), "accessible by others") || !strings.Contains(err.Error(), "0400") {
			t.Errorf("identity at %04o: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400} {
		if err := os.Chmod(filepath.Join(b.Dir(), "identity"), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Open(b.Dir(), e.opts()); err != nil {
			t.Errorf("identity at %04o: %v", mode, err)
		}
	}
	keys, err := filepath.Glob(filepath.Join(b.Dir(), "keys", "prekeys", "*.agekey"))
	if err != nil || len(keys) == 0 {
		t.Fatal(keys, err)
	}
	a := e.heldMailbox()
	send(t, a, b, "waits")
	if err := os.Chmod(keys[0], 0o644); err != nil { //nolint:gosec // open on purpose
		t.Fatal(err)
	}
	if _, err := b.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), "accessible by others") {
		t.Errorf("a prekey at 0644: %v", err)
	}
}

// A config file others can write is refused, one others can only read is not: a
// ConfigMap arrives 0644.
func TestAConfigFileOthersCanWriteIsRefused(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, bad := range map[os.FileMode]bool{0o600: false, 0o644: false, 0o444: false, 0o664: true, 0o646: true} {
		if err := os.Chmod(file, mode); err != nil {
			t.Fatal(err)
		}
		if err := client.CheckConfigFile(file); (err != nil) != bad {
			t.Errorf("%04o: %v", mode, err)
		}
	}
}

// inbox-dir on another filesystem than the rest of the mail: allowing a sender
// moves its held mail there, where a rename cannot. Where this machine has no second
// filesystem to write to, there is nothing to try.
func TestAllowingASenderCrossesFilesystems(t *testing.T) {
	t.Parallel()
	const other = "/dev/shm"
	root := t.TempDir()
	here, err1 := os.Stat(root)
	there, err2 := os.Stat(other)
	if err1 != nil || err2 != nil || here.Sys().(*syscall.Stat_t).Dev == there.Sys().(*syscall.Stat_t).Dev {
		return
	}
	inbox, err := os.MkdirTemp(other, "dromas-test-")
	if err != nil {
		return
	}
	t.Cleanup(func() { _ = os.RemoveAll(inbox) })
	e := newEnv(t)
	opt := e.opts()
	opt.Layout.Inbox = inbox
	b, err := client.Init(t.Context(), filepath.Join(root, "base"), opt)
	if err != nil {
		t.Fatal(err)
	}
	a := e.heldMailbox()
	send(t, a, b, "waits")
	if res := receive(t, b); len(res.Held) != 1 {
		t.Fatalf("%+v", res)
	}
	if _, err := b.Allow(t.Context(), a.Address()); err != nil {
		t.Fatal(err)
	}
	if n, _ := filepath.Glob(filepath.Join(inbox, "*.json")); len(n) != 1 {
		t.Errorf("inbox-dir holds %v", n)
	}
	if n, _ := filepath.Glob(filepath.Join(root, "base", "mail", "held", "*.json")); len(n) != 0 {
		t.Errorf("held/ still holds %v", n)
	}
}
