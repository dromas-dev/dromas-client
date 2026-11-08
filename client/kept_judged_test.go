package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// Each directory that keeps a message recv passed, inbox-dir and held/, that
// others can write is refused by a command that writes the mailbox and by a
// reading, naming it, with nothing acked: whoever can write there could put in
// a message that reads as delivered (#717). The line the refusal gives puts it
// right.
func TestAKeptDirectoryOthersCanWriteIsRefused(t *testing.T) {
	t.Parallel()
	for _, name := range client.KeptDirs() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.heldMailbox()
			send(t, a, b, "waits")
			path := b.Path(name)
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			openToWrites(t, path)
			var refusal error
			for what, run := range map[string]func() error{
				"recv":  func() error { _, err := b.Receive(ctx); return err },
				"inbox": func() error { _, err := b.Inbox(ctx); return err },
				"held":  func() error { _, err := b.HeldMessages(ctx); return err },
			} {
				err := run()
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "reads as delivered") {
					t.Fatalf("%s with %s open to others' writes: %v", what, path, err)
				}
				refusal = err
			}
			if n := e.srv.Count(b.Address()); n != 1 {
				t.Fatalf("the server holds %d, and nothing was to be acked", n)
			}
			runFix(t, fixLine(t, refusal))
			if res := receive(t, b); len(res.Delivered)+len(res.Held) != 1 {
				t.Fatalf("recv once the directory is put right: %+v", res)
			}
		})
	}
}

// A message file inbox-dir or held/ keeps that others can reach is refused
// before it is read as a delivered message, naming it, by each command that
// prints or moves one, an owed loop's handover of it among them, and the line
// the refusal gives puts it right (#717, #774).
func TestAKeptMessageOthersCanReachIsRefused(t *testing.T) {
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
		if err == nil || !strings.Contains(err.Error(), fixWord) {
			t.Fatalf("%s with a kept file open to others: %v", what, err)
		}
		if !strings.Contains(err.Error(), inPath) && !strings.Contains(err.Error(), heldPath) {
			t.Fatalf("%s names neither file: %v", what, err)
		}
	}
	_, err := b.Inbox(ctx)
	runFix(t, fixLine(t, err))
	if got, err := b.Inbox(ctx); err != nil || len(got) != 1 {
		t.Fatalf("inbox once the file is put right: %v, %v", got, err)
	}
}

// Options.UnsafeInboxPermissions reads a kept directory others can write and a
// kept message others can reach, and Options.UnsafeStatePermissions opens a
// state file others can reach. Neither leaves a private key unjudged.
func TestTheUnsafeOptionsLeaveTheirFilesUnjudged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "kept")
	in := receive(t, b).Delivered[0]
	openToWrites(t, b.Path("inbox"))
	openFile(t, b.Path("inbox", in.ID+".json"))
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
