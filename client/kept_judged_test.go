package client_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
)

// busyMailbox is a held mailbox b of e with every directory of its layout in
// use: a message sent and one that failed for good, in sent/ and
// outbox/failed/; an entry waiting in outbox/ for a; and a message of a's
// waiting on the server, unacked.
func busyMailbox(t *testing.T) (e *env, a, b *client.Mailbox) {
	t.Helper()
	ctx := context.Background()
	e = newEnv(t)
	a, b, c := e.mailbox(), e.heldMailbox(), e.mailbox()
	send(t, b, a, "sent")
	revoke(t, e, c)
	if _, err := b.Send(ctx, c.Address(), "to a revoked mailbox"); err == nil || !strings.Contains(err.Error(), "outbox/failed") {
		t.Fatalf("a send that fails for good: %v", err)
	}
	forge.FailOnce(e.srv, http.MethodPost, "/v1/"+a.Address().String()+"/messages")
	if _, err := b.Send(ctx, a.Address(), "waits in outbox/"); err == nil {
		t.Fatal("the send to wait went through")
	}
	e.srv.SetMiddle(nil)
	send(t, a, b, "waits")
	return e, a, b
}

// mailboxDirs are b's directories, by their paths inside its base directory:
// each its layout names (client.LayoutDirs) and each found in it on disk. None
// is left out, so a directory that judgedDirs leaves out fails the test that
// reads this.
func mailboxDirs(t *testing.T, b *client.Mailbox) []string {
	t.Helper()
	var dirs []string
	add := func(p string) {
		rel, err := filepath.Rel(b.Dir(), p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Fatalf("%s is outside the base directory %s", p, b.Dir())
		}
		if !slices.Contains(dirs, rel) {
			dirs = append(dirs, rel)
		}
	}
	for _, d := range client.LayoutDirs(b) {
		add(d)
	}
	if err := filepath.WalkDir(b.Dir(), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			add(p)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(dirs)
	return dirs
}

// Each directory of the mailbox that others can write is refused by a command
// that writes the mailbox and by a reading, naming it, with nothing acked and
// nothing sent: whoever can write there could put in or rename a file the
// mailbox takes as its owner's, a list, a key, a message waiting to be sent,
// or one that reads as delivered (#717). The line the refusal gives puts it
// right.
func TestAMailboxDirectoryOthersCanWriteIsRefused(t *testing.T) {
	t.Parallel()
	_, _, probe := busyMailbox(t)
	dirs := mailboxDirs(t, probe)
	if len(dirs) < 11 {
		t.Fatalf("%d directories: too few for a mailbox with every folder in use: %q", len(dirs), dirs)
	}
	for _, rel := range dirs {
		name := filepath.ToSlash(rel)
		if name == "." {
			name = "base-dir"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e, a, b := busyMailbox(t)
			path := filepath.Join(b.Dir(), rel)
			openToWrites(t, path)
			sentToA := e.srv.Count(a.Address())
			var refusal error
			for what, run := range map[string]func() error{
				"recv":         func() error { _, err := b.Receive(ctx); return err },
				"inbox":        func() error { _, err := b.Inbox(ctx); return err },
				"held":         func() error { _, err := b.HeldMessages(ctx); return err },
				"outbox flush": func() error { _, err := b.Flush(ctx); return err },
			} {
				err := run()
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "takes as its owner's") {
					t.Fatalf("%s with %s open to others' writes: %v", what, path, err)
				}
				refusal = err
			}
			if n, m := e.srv.Count(b.Address()), e.srv.Count(a.Address()); n != 1 || m != sentToA {
				t.Fatalf("the servers hold %d for b and %d for a, from %d: something was acked or sent", n, m, sentToA)
			}
			runFix(t, fixLine(t, refusal))
			if res := receive(t, b); len(res.Delivered)+len(res.Held) != 1 {
				t.Fatalf("recv once the directory is put right: %+v", res)
			}
		})
	}
}

// An outbox entry others can write is refused before it is read, naming it,
// and nothing is sent: whoever can write it could have the mailbox sign and
// send what they wrote as its owner's (#717). Once put right it is sent.
func TestAnOutboxEntryOthersCanWriteIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e, a, b := busyMailbox(t)
	entries, err := filepath.Glob(filepath.Join(b.Dir(), "outbox", "*.P"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("outbox: %q, %v", entries, err)
	}
	before := e.srv.Count(a.Address())
	openFileToWrites(t, entries[0])
	if _, err := b.Flush(ctx); err == nil || !strings.Contains(err.Error(), entries[0]) || !strings.Contains(err.Error(), "writable by") {
		t.Fatalf("flush of an entry others can write: %v", err)
	}
	if n := e.srv.Count(a.Address()); n != before {
		t.Fatalf("the entry was sent: %d at the server, from %d", n, before)
	}
	openFile(t, entries[0])
	if sent, err := b.Flush(ctx); err != nil || len(sent) != 1 {
		t.Fatalf("flush once the entry is put right: %v, %v", sent, err)
	}
}

// A message file inbox-dir or held/ keeps that others can write is refused
// before it is read as a delivered message, naming it, by each command that
// prints or moves one, an owed loop's handover of it among them (#717, #774).
// One others can only read is read.
func TestAKeptMessageOthersCanWriteIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, c := e.mailbox(), e.mailbox(), e.heldMailbox()
	send(t, a, b, "kept")
	in := owedDelivery(t, b)
	inPath := b.Path("inbox", in.ID+".json")
	send(t, a, c, "held")
	held := receive(t, c)
	if len(held.Held) != 1 {
		t.Fatalf("held: %+v", held)
	}
	heldPath := c.Path("held", held.Held[0].ID+".json")
	for _, p := range []string{inPath, heldPath} {
		openFile(t, p)
	}
	if got, err := b.Inbox(ctx); err != nil || len(got) != 1 {
		t.Fatalf("inbox list with a kept file open to others' reads: %v, %v", got, err)
	}
	if got, err := c.HeldMessages(ctx); err != nil || len(got) != 1 {
		t.Fatalf("held list with a kept file open to others' reads: %v, %v", got, err)
	}
	for _, p := range []string{inPath, heldPath} {
		openFileToWrites(t, p)
	}
	for what, run := range map[string]func() error{
		"inbox list": func() error { _, err := b.Inbox(ctx); return err },
		"inbox show": func() error { _, err := b.InboxMessage(ctx, in.ID); return err },
		"held list":  func() error { _, err := c.HeldMessages(ctx); return err },
		"peer allow": func() error { _, err := c.Allow(ctx, a.Address()); return err },
		"an owed handover": func() error {
			return b.Follow(ctx, oneDrain(true), func(_ context.Context, ev client.Event) error {
				if ev.Kind == client.EventDelivery {
					t.Errorf("an owed loop handed over %s", ev.Delivery.ID)
				}
				return nil
			})
		},
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "writable by") {
			t.Fatalf("%s with a kept file open to others' writes: %v", what, err)
		}
		if !strings.Contains(err.Error(), inPath) && !strings.Contains(err.Error(), heldPath) {
			t.Fatalf("%s names neither file: %v", what, err)
		}
	}
	openFile(t, inPath)
	if got, err := b.Inbox(ctx); err != nil || len(got) != 1 {
		t.Fatalf("inbox once the file is put right: %v, %v", got, err)
	}
}

// Options.UnsafeInboxPermissions reads a kept directory and a kept message
// others can write, and Options.UnsafeStatePermissions opens a
// state file others can reach. Neither leaves a private key unjudged.
func TestTheUnsafeOptionsLeaveTheirFilesUnjudged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "kept")
	in := receive(t, b).Delivered[0]
	openToWrites(t, b.Path("inbox"))
	openFileToWrites(t, b.Path("inbox", in.ID+".json"))
	openFile(t, filepath.Join(b.Dir(), "state"))
	opt := e.opts()
	if s, err := client.Open(b.Dir(), opt); err == nil {
		if _, err := s.Inbox(ctx); err == nil {
			t.Fatal("the files were read with neither option")
		}
	}
	opt.UnsafeInboxPermissions, opt.UnsafeStatePermissions = true, true
	u := mustOpen(t, b.Dir(), opt)
	if got, err := u.Inbox(ctx); err != nil || len(got) != 1 {
		t.Fatalf("inbox with both options: %v, %v", got, err)
	}
	if _, err := u.InboxMessage(ctx, in.ID); err != nil {
		t.Fatalf("inbox show with both options: %v", err)
	}
	openFile(t, filepath.Join(b.Dir(), "identity"))
	if _, err := client.Open(b.Dir(), opt); err == nil || !strings.Contains(err.Error(), "accessible by") {
		t.Fatalf("an identity others can reach, with both options: %v", err)
	}
}

func mustOpen(t *testing.T, dir string, opt client.Options) *client.Mailbox {
	t.Helper()
	b, err := client.Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// owedDelivery is the one message b's server holds, delivered by an owed loop
// whose handler fails, so the message stays owed its handling.
func owedDelivery(t *testing.T, b *client.Mailbox) client.Delivery {
	t.Helper()
	boom := errors.New("boom")
	var got []client.Delivery
	if err := b.Follow(context.Background(), oneDrain(true), func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			got = append(got, *ev.Delivery)
			return boom
		}
		return nil
	}); !errors.Is(err, boom) || len(got) != 1 {
		t.Fatalf("an owed loop: %v, having handed over %d", err, len(got))
	}
	if st, err := b.Status(context.Background()); err != nil || st.Owed != 1 {
		t.Fatalf("after: %+v, %v", st, err)
	}
	return got[0]
}
