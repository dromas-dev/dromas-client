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

// planted is path as a file others can reach, made empty where it is not there.
func planted(t *testing.T, path string) {
	t.Helper()
	if !exists(path) {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	openFile(t, path)
}

// fixLine is the line a refusal gives to run.
func fixLine(t *testing.T, err error) string {
	t.Helper()
	_, line, ok := strings.Cut(err.Error(), "Run: ")
	if !ok {
		t.Fatalf("no line to run in: %v", err)
	}
	return line
}

// Each private file of the base directory, read from the code, opened to others:
// a command that writes the mailbox, a reading and a renewal each refuse it,
// naming it, with nothing acked. The line the refusal gives puts it right.
func TestEveryPrivateFileIsJudgedBeforeEveryOpen(t *testing.T) {
	t.Parallel()
	for _, f := range client.PrivateFiles() {
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "waits")
			path := filepath.Join(b.Dir(), f)
			existed := exists(path)
			planted(t, path)
			var refusal error
			for name, run := range map[string]func() error{
				"recv":   func() error { _, err := b.Receive(ctx); return err },
				"status": func() error { _, err := b.Status(ctx); return err },
				"inbox":  func() error { _, err := b.Inbox(ctx); return err },
				"renew":  func() error { _, err := b.Rekey(ctx); return err },
			} {
				err := run()
				if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), fixWord) {
					t.Fatalf("%s with %s open to others: %v", name, f, err)
				}
				refusal = err
			}
			if n := e.srv.Count(b.Address()); n != 1 {
				t.Fatalf("the server holds %d, and nothing was to be acked", n)
			}
			runFix(t, fixLine(t, refusal))
			if !existed { // a file that was never there holds no partial or list
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			if res := receive(t, b); len(res.Delivered) != 1 {
				t.Fatalf("recv once the file is put right: %+v", res)
			}
		})
	}
}

// A file already there before init is judged, so one another user planted to
// read what the mailbox writes there is refused, and init makes no identity.
func TestAFilePlantedBeforeInitIsRefused(t *testing.T) {
	t.Parallel()
	for _, f := range client.PrivateFiles() {
		t.Run(f, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			dir := t.TempDir()
			path := filepath.Join(dir, f)
			planted(t, path)
			if _, err := client.Init(t.Context(), dir, e.opts()); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), fixWord) {
				t.Fatalf("init over a planted %s: %v", f, err)
			}
			if exists(filepath.Join(dir, "identity")) {
				t.Fatal("init went on to make the identity")
			}
		})
	}
}

// A state file others can reach is refused before bbolt reads a byte of it: these
// bytes are no database, which bbolt would be first to say.
func TestAStateFileIsJudgedBeforeBboltReadsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	state := filepath.Join(b.Dir(), "state")
	if err := os.WriteFile(state, []byte("no database"), 0o600); err != nil {
		t.Fatal(err)
	}
	openFile(t, state)
	for name, err := range map[string]error{
		"a command": func() error { _, err := b.Receive(t.Context()); return err }(),
		"a reading": func() error { _, err := b.Status(t.Context()); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), fixWord) || strings.Contains(err.Error(), "the mailbox's state file:") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// An alias is read with no command holding the mailbox, so it is judged on its
// own: a name others could change is no name to send by.
func TestAnAliasIsJudgedBeforeItIsRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	if err := b.SetAlias(ctx, "friend", a.Address()); err != nil {
		t.Fatal(err)
	}
	if got, err := b.Destination("friend"); err != nil || got != a.Address() {
		t.Fatalf("the alias: %v, %v", got, err)
	}
	path := filepath.Join(b.Dir(), "aliases")
	openFile(t, path)
	if _, err := b.Destination("friend"); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("the alias with its file open to others: %v", err)
	}
}

// identity.doc and address hold nothing private, so others reaching them stops
// nothing, and the mailbox goes on. They are the only two of the base directory's
// files no open judges, so a new file is judged unless it is named here too.
func TestAPublicFileIsNotJudged(t *testing.T) {
	t.Parallel()
	if got := client.PublicFiles(); !slices.Equal(got, []string{"identity.doc", "address"}) {
		t.Errorf("the files no open judges: %q", got)
	}
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "waits")
	for _, f := range []string{"identity.doc", "address"} {
		openFile(t, filepath.Join(b.Dir(), f))
	}
	openFile(t, b.Path("identity.pub"))
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("recv: %+v", res)
	}
}
