//go:build unix

package client_test

import (
	"os"
	"os/exec"
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

// A working key others can reach is refused where it signs: a send, whose pass and
// message it signs.
func TestAWorkingKeyOthersCanReachIsRefused(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	a, b := e.mailbox(), e.mailbox()
	if !certified(t, a) {
		t.Fatal("not under a certificate")
	}
	keys, err := filepath.Glob(filepath.Join(a.Dir(), "keys", "work", "*"))
	if err != nil || len(keys) == 0 {
		t.Fatal(keys, err)
	}
	for _, k := range keys {
		if !strings.Contains(filepath.Base(k), ".") {
			if err := os.Chmod(k, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := a.Send(t.Context(), b.Address(), "x"); err == nil || !strings.Contains(err.Error(), "accessible by others") {
		t.Errorf("send: %v", err)
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
// filesystem to write to, there is nothing to try, and the test skips: every system
// but Linux has none here, so scripts/check.sh's skip check fails a skip on Linux.
func TestAllowingASenderCrossesFilesystems(t *testing.T) {
	t.Parallel()
	const other = "/dev/shm"
	root := t.TempDir()
	here, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	there, err := os.Stat(other)
	if err != nil {
		t.Skipf("no second filesystem to write to at %s: %v", other, err)
	}
	if here.Sys().(*syscall.Stat_t).Dev == there.Sys().(*syscall.Stat_t).Dev {
		t.Skipf("no second filesystem to write to at %s: it is the temporary directory's", other)
	}
	inbox, err := os.MkdirTemp(other, "dromas-test-")
	if err != nil {
		t.Skipf("no second filesystem to write to at %s: %v", other, err)
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

// A config file its user does not own is refused, as ssh refuses ~/.ssh/config: its
// owner can write it whatever its mode says. Root's is taken: a ConfigMap is
// root's, and root can write anything.
func TestAConfigFileAnotherUserOwnsIsRefused(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	if me == 0 { // a file owned by neither root nor the user the test says it is
		if err := os.Chown(file, 1, 1); err != nil {
			t.Fatal(err)
		}
		me = 1
	}
	if err := client.CheckConfigFileAs(file, me); err != nil {
		t.Errorf("its own user's: %v", err)
	}
	if err := client.CheckConfigFileAs(file, me+1); err == nil || !strings.Contains(err.Error(), "owned by another user") {
		t.Errorf("another user's: %v", err)
	}
	if err := client.CheckConfigFileAs("/etc/passwd", me+1); err != nil {
		t.Errorf("root's: %v", err)
	}
}

// A mailbox's private file another user owns is refused, whatever its mode says:
// they can read and change it. Root's is not, as a config file's is not.
func TestAPrivateFileAnotherUserOwnsIsRefused(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	if me == 0 { // a file owned by neither root nor the user the test says it is
		if err := os.Chown(file, 1, 1); err != nil {
			t.Fatal(err)
		}
		me = 1
	}
	if err := client.CheckPrivateFileAs(file, me); err != nil {
		t.Errorf("its own user's: %v", err)
	}
	if err := client.CheckPrivateFileAs(file, me+1); err == nil || !strings.Contains(err.Error(), "owned by another user") {
		t.Errorf("another user's: %v", err)
	}
}

// A private file and a kept directory, each both open to others and owned by
// another user, are refused for their owner: only a file's owner can change its
// mode, so a chmod line there is one the mailbox's user cannot run. Once the
// owner gives it to the mailbox's user, the refusal's own line, run, leaves one
// that passes.
func TestTheOwnerIsJudgedBeforeTheMode(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		mode  os.FileMode
		make  func(path string, mode os.FileMode) error
		check func(path string, me int) error
	}{
		{"a private file", 0o644, func(p string, m os.FileMode) error { return os.WriteFile(p, nil, m) }, client.CheckPrivateFileAs},
		{"a kept directory", 0o777, os.Mkdir, client.CheckKeptDirAs},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "judged")
			if err := c.make(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, c.mode); err != nil {
				t.Fatal(err)
			}
			owner := os.Getuid()
			if owner == 0 { // owned by neither root nor the user the test says it is
				if err := os.Chown(path, 1, 1); err != nil {
					t.Fatal(err)
				}
				owner = 1
			}
			err := c.check(path, owner+1)
			if err == nil || !strings.Contains(err.Error(), "owned by another user") || strings.Contains(err.Error(), "Run: ") {
				t.Fatalf("judged as another user's: %v; want the refusal of its owner, with no line to run", err)
			}
			err = c.check(path, owner)
			if err == nil {
				t.Fatalf("its owner's at %04o passed", c.mode)
			}
			_, line, ok := strings.Cut(err.Error(), "Run: ")
			if !ok {
				t.Fatalf("its owner's at %04o: %v; want a line to run", c.mode, err)
			}
			if out, err := exec.Command("sh", "-c", line).CombinedOutput(); err != nil { //nolint:gosec // the refusal's own line
				t.Fatalf("%s: %v %s", line, err, out)
			}
			if err := c.check(path, owner); err != nil {
				t.Errorf("after the refusal's own line (%s): %v", line, err)
			}
		})
	}
}

// A mail directory that cannot be read says so: it never reads as an empty one.
func TestAFolderThatCannotBeReadIsNotEmpty(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		return // root reads it anyway
	}
	e := newEnv(t)
	b := e.mailbox()
	held := filepath.Join(b.Dir(), "mail", "held")
	if err := os.Chmod(held, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(held, 0o700) })
	if _, err := b.HeldMessages(t.Context()); err == nil {
		t.Error("a folder that cannot be read was listed as empty")
	}
	if _, err := b.Status(t.Context()); err == nil {
		t.Error("status counted a folder that cannot be read as empty")
	}
}
