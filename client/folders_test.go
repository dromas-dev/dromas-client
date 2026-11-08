package client_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// moveAside moves a folder of b where a volume that is not mounted would leave
// it, and gives back how to bring it back.
func moveAside(t *testing.T, path string) (back func()) {
	t.Helper()
	aside := filepath.Join(t.TempDir(), "aside")
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Rename(aside, path); err != nil {
			t.Fatal(err)
		}
	}
}

// Each folder that may be on a volume of its own, moved aside after init,
// stops recv before anything is listed, fetched or acked: the server still holds
// the message, the folder is not made again, and the words name it. Once it
// is back, the message is delivered.
func TestAFolderMovedAsideStopsRecvWithNothingAcked(t *testing.T) {
	t.Parallel()
	for _, folder := range client.BoundFolders() {
		t.Run(folder, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "waiting on the server")
			back := moveAside(t, b.Path(folder))
			_, err := b.Receive(context.Background())
			if err == nil || !strings.Contains(err.Error(), b.Path(folder)) || !strings.Contains(err.Error(), "not be mounted") || !strings.Contains(err.Error(), "removed on purpose") {
				t.Fatalf("recv with %s moved aside: %v", folder, err)
			}
			if n := e.srv.Count(b.Address()); n != 1 {
				t.Fatalf("the server holds %d, and nothing was to be acked", n)
			}
			if exists(b.Path(folder)) {
				t.Fatalf("%s was made again", folder)
			}
			back()
			if res := receive(t, b); len(res.Delivered) != 1 || e.srv.Count(b.Address()) != 0 {
				t.Fatalf("recv with %s back: %+v", folder, res)
			}
		})
	}
}

// A separate inbox-dir is bound as the inbox it is, and an unmounted mail-dir
// names every folder under it.
func TestASeparateInboxDirAndAWholeMailDirAreBound(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	root := t.TempDir()
	opt := e.opts()
	opt.Layout = client.Layout{Mail: filepath.Join(root, "volume", "mail"), Inbox: filepath.Join(root, "agent", "inbox")}
	ctx := context.Background()
	b, err := client.Init(ctx, filepath.Join(root, "base"), opt)
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, b)
	a := e.mailbox()
	send(t, a, b, "waiting on the server")
	back := moveAside(t, opt.Layout.Inbox)
	if _, err := b.Receive(ctx); err == nil || !strings.Contains(err.Error(), opt.Layout.Inbox) {
		t.Fatalf("recv with inbox-dir moved aside: %v", err)
	}
	back()
	back = moveAside(t, opt.Layout.Mail)
	_, err = b.Receive(ctx)
	if err == nil || e.srv.Count(b.Address()) != 1 {
		t.Fatalf("recv with mail-dir moved aside: %v; the server holds %d", err, e.srv.Count(b.Address()))
	}
	for _, f := range []string{"held", "refused", "sent"} {
		if !strings.Contains(err.Error(), b.Path(f)) {
			t.Errorf("the refusal does not name %s: %v", f, err)
		}
	}
	back()
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("recv with both back: %+v", res)
	}
}

// outbox/ is beside the state file, so its absence says nothing of a volume:
// the next command makes it again.
func TestAMissingOutboxIsMadeAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	if err := os.RemoveAll(b.Path("outbox")); err != nil {
		t.Fatal(err)
	}
	receive(t, b)
	if !exists(b.Path("outbox")) {
		t.Fatal("outbox/ was not made again")
	}
}

// A folder its owner removed on purpose is made again by hand, as the refusal
// says, and the mailbox goes on.
func TestAFolderRemovedOnPurposeIsMadeAgainByItsOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	if err := os.RemoveAll(b.Path("refused")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(context.Background()); err == nil {
		t.Fatal("recv with refused/ removed went on")
	}
	if err := os.Mkdir(b.Path("refused"), 0o700); err != nil {
		t.Fatal(err)
	}
	send(t, a, b, "after")
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("recv with refused/ made again: %+v", res)
	}
}

// A reading of a folder refuses as a command does, since a folder not mounted
// would read as empty, and status names the folder in what needs a look.
func TestAReadingOfAMissingFolderIsRefusedAndStatusSaysSo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	for folder, read := range map[string]func() error{
		"inbox":   func() error { _, err := b.Inbox(ctx); return err },
		"held":    func() error { _, err := b.HeldMessages(ctx); return err },
		"refused": func() error { _, err := b.RefusedMessages(ctx); return err },
	} {
		back := moveAside(t, b.Path(folder))
		if err := read(); err == nil || !strings.Contains(err.Error(), b.Path(folder)) {
			t.Errorf("the reading with %s moved aside: %v", folder, err)
		}
		st, err := b.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, b.Path(folder)) }) {
			t.Errorf("status with %s moved aside: %q", folder, st.Attention)
		}
		back()
	}
}

// A new state file, which a restore makes, starts without the record: its first
// command makes what is missing, the record then binds, and a mailbox restored
// from its identity alone works.
func TestARestoreMakesItsFoldersAndThenBindsThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	identityAlone(t, b.Dir())
	r, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Rekey(ctx); err != nil {
		t.Fatalf("the restore: %v", err)
	}
	landAll(t, r)
	send(t, a, r, "after the restore")
	if res := receive(t, r); len(res.Delivered) != 1 {
		t.Fatalf("recv after the restore: %+v", res)
	}
	defer moveAside(t, r.Path("held"))()
	if _, err := r.Receive(ctx); err == nil || !strings.Contains(err.Error(), r.Path("held")) {
		t.Fatalf("recv with held/ moved aside after the restore: %v", err)
	}
}

// A state file lost beside a missing folder is a new state file: its first
// command makes the folder again, before it refuses a state that does not name
// the prekeys kept.
func TestANewStateFileMakesAMissingFolder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(b.Path("held")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rekey(context.Background()); !client.NotTheKeys(err) {
		t.Fatalf("the renewal on a new state file: %v", err)
	}
	if !exists(b.Path("held")) {
		t.Fatal("held/ was not made")
	}
}
