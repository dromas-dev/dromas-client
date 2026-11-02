package client_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// Every file and directory of a mailbox the client made is its owner's alone: each
// directory by private, mode 0700 on Unix and an access list on Windows, which
// what is made in it inherits, and each file written 0600. The set is read from
// the mailbox's own directories once it has sent and received, so a file added
// later is held to it too.
func TestEveryFileOfAMailboxIsItsOwnersAlone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	made := filepath.Join(t.TempDir(), "made")
	a, err := client.Init(t.Context(), made, e.opts())
	if err != nil {
		t.Fatal(err)
	}
	b := e.mailbox()
	for _, x := range [][2]*client.Mailbox{{a, b}, {b, a}} {
		landAll(t, x[1])
		if _, err := x[0].Send(t.Context(), x[1].Address(), "kept where only its owner reads it"); err != nil {
			t.Fatal(err)
		}
		if _, err := x[1].Receive(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	err = filepath.WalkDir(a.Dir(), func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		n++
		if err := ownerOnly(p); err != nil {
			rel, _ := filepath.Rel(a.Dir(), p)
			t.Errorf("%s: %v", filepath.ToSlash(rel), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 15 {
		t.Errorf("the mailbox held %d files and directories: too few for one that sent and received", n)
	}
}

// A directory the client did not make stays as its maker left it, as ssh(1) leaves
// ~/.ssh: only a key file is judged. A mailbox made in one, and one restored into
// one, work, and neither changes it.
func TestADirectoryTheClientDidNotMakeIsLeftAsItIs(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	made := filepath.Join(t.TempDir(), "made")
	openToOthers(t, made)
	openToOthers(t, filepath.Join(made, "mail", "inbox"))
	a, err := client.Init(t.Context(), made, e.opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{made, filepath.Join(made, "mail", "inbox")} {
		if ownerOnly(d) == nil {
			t.Errorf("%s was made its owner's alone", d)
		}
	}
	if err := ownerOnly(filepath.Join(made, "identity")); err != nil {
		t.Errorf("the identity it wrote: %v", err)
	}
	if err := ownerOnly(filepath.Join(made, "keys", "prekeys")); err != nil {
		t.Errorf("prekeys/, which it made: %v", err)
	}
}

// openToOthers makes dir as someone else might before a mailbox is made in it,
// open to others (widen), and fails the test where ownerOnly still passes
// it: a mailbox made there would then pass whatever private did.
func openToOthers(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	widen(t, dir)
	if ownerOnly(dir) == nil {
		t.Fatalf("%s is its owner's alone, opened to others: no mailbox made in it could fail this test", dir)
	}
}
