package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// A message from a sender the mailbox has not allowed passes every check and is held:
// acked, kept in held/, never delivered. allow delivers what it held, and
// the sender's next message lands in inbox/.
func TestAMessageFromASenderNotAllowedIsHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "first")
	send(t, a, b, "second")
	res := receive(t, b)
	if len(res.Delivered) != 0 || len(res.Held) != 2 || len(res.Refused) != 0 || e.srv.Count(b.Address()) != 0 {
		t.Fatalf("a stranger's message: %+v, %d left on the server", res, e.srv.Count(b.Address()))
	}
	if inbox, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "inbox", "*.json")); len(inbox) != 0 {
		t.Fatalf("inbox/ holds %v", inbox)
	}
	st, err := b.Status(t.Context())
	if err != nil || st.Held != 2 || !strings.Contains(strings.Join(st.Attention, "\n"), "2 held in held/") {
		t.Fatalf("status: %+v, %v", st, err)
	}
	held, err := b.HeldMessages(t.Context())
	if err != nil || len(held) != 1 || held[0].From != a.Address() || len(held[0].IDs) != 2 || held[0].FirstSentAt == "" {
		t.Fatalf("held: %+v, %v", held, err)
	}
	moved, err := b.Allow(ctx, a.Address())
	if err != nil || len(moved) != 2 || bodyOf(t, moved[0])+bodyOf(t, moved[1]) != "firstsecond" && bodyOf(t, moved[0])+bodyOf(t, moved[1]) != "secondfirst" {
		t.Fatalf("allow: %+v, %v", moved, err)
	}
	if left, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "held", "*.json")); len(left) != 0 {
		t.Fatalf("held/ still holds %v", left)
	}
	if inbox, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "inbox", "*.json")); len(inbox) != 2 {
		t.Fatalf("inbox/ holds %v", inbox)
	}
	send(t, a, b, "third")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Held) != 0 || bodyOf(t, res.Delivered[0]) != "third" {
		t.Fatalf("a message from a sender allowed: %+v", res)
	}
	// Under the default policy the server was told nothing: the mailbox blocks
	// nobody and lets every sender reach it, which is the server's own
	// default.
	if e.srv.ACL(b.Address()) != nil {
		t.Fatalf("an ACL reached the server: %+v", e.srv.ACL(b.Address()))
	}
}

// Writing to a mailbox allows it, so its reply lands in inbox/; so does naming
// it, and un-naming it is not blocking it.
func TestWritingOrNamingAPeerAllowsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b, c := e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
	send(t, b, a, "hello")
	send(t, a, b, "the reply")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Held) != 0 {
		t.Fatalf("a reply from a mailbox written to: %+v", res)
	}
	if err := b.SetAlias(t.Context(), "carol", c.Address()); err != nil {
		t.Fatal(err)
	}
	if err := b.RemoveAlias(t.Context(), "carol"); err != nil {
		t.Fatal(err)
	}
	send(t, c, b, "from a mailbox once named")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Held) != 0 {
		t.Fatalf("a message from a mailbox named, then un-named: %+v", res)
	}
	// An alias written by hand, which allowed never heard of, allows too.
	d := e.heldMailbox()
	f, err := os.OpenFile(filepath.Join(b.Dir(), "aliases"), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("dan " + d.Address().String() + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	send(t, d, b, "named by hand")
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("a message from a mailbox an alias written by hand names: %+v", res)
	}
}

// Block deletes what held/ keeps from a sender and tells the server, which
// refuses its later sends: final, so the sender's client moves the message
// to outbox/failed/. What the server holds from it stays there (PROTOCOL.md
// §11.14), and the next drain drops it. Blocking wins over allowing.
func TestBlockDropsASendersMessagesHereAndAtTheServer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "held")
	receive(t, b)
	send(t, a, b, "waiting on the server")
	if _, err := b.Allow(ctx, a.Address()); err != nil { // and blocking still wins
		t.Fatal(err)
	}
	n, err := b.Block(ctx, a.Address())
	if err != nil || n != 0 {
		t.Fatalf("block: %d, %v", n, err) // allow moved the held one to inbox/ already
	}
	acl := e.srv.ACL(b.Address())
	if acl == nil || !acl.Deny.Has(a.Address()) || !acl.Allow.Any || e.srv.Count(b.Address()) != 1 {
		t.Fatalf("the server holds %+v and %d messages, want one", acl, e.srv.Count(b.Address()))
	}
	_, err = a.Send(ctx, b.Address(), "refused")
	if client.Code(err) != protocol.CodeSenderNotAllowed || !strings.Contains(err.Error(), "moved to outbox/failed/") {
		t.Fatalf("a send to a mailbox that blocked its sender: %v", err)
	}
	if failed, _ := filepath.Glob(filepath.Join(a.Dir(), "outbox", "failed", "*.P")); len(failed) != 1 {
		t.Fatalf("outbox/failed/ holds %v", failed)
	}
	if res := receive(t, b); len(res.Blocked) != 1 || len(res.Held)+len(res.Delivered) != 0 || e.srv.Count(b.Address()) != 0 {
		t.Fatalf("the drain after the block: %+v, and %d messages on the server", res, e.srv.Count(b.Address()))
	}

	// What held/ keeps from a sender goes when it is blocked.
	c := e.heldMailbox()
	send(t, c, b, "from c")
	receive(t, b)
	if n, err := b.Block(ctx, c.Address()); err != nil || n != 1 {
		t.Fatalf("block c: %d, %v", n, err)
	}
	if left, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "held", "*.json")); len(left) != 0 {
		t.Fatalf("held/ still holds %v", left)
	}
	if blocked, err := b.Blocked(t.Context()); err != nil || len(blocked) != 2 {
		t.Fatalf("blocked: %v, %v", blocked, err)
	}
}

// A message from a blocked sender that the server still delivers, its ACL not
// yet taken, is dropped by the mailbox itself, and the drain says so.
func TestAMailboxDropsMessagesFromASenderItBlockedWhateverItsServerDoes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "sent before the block")
	if err := os.WriteFile(filepath.Join(b.Dir(), "blocked"), []byte(a.Address().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.HasSuffix(r.URL.Path, "/acl") {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
	res := receive(t, b)
	if len(res.Blocked) != 1 || len(res.Held)+len(res.Delivered) != 0 || res.ACLErr == nil || e.srv.Count(b.Address()) != 0 {
		t.Fatalf("a drain with the sender blocked here only: %+v", res)
	}
	if st, err := b.Status(t.Context()); err != nil || !strings.Contains(strings.Join(st.Attention, "\n"), "holds no ACL this mailbox sent") {
		t.Fatalf("status: %+v, %v", st, err)
	}
	e.srv.SetMiddle(nil)
	if res := receive(t, b); res.ACLErr != nil || !e.srv.ACL(b.Address()).Deny.Has(a.Address()) {
		t.Fatalf("the next drain sends the ACL: %+v", res)
	}
}

// "unknown inbox" lands every sender's messages in inbox/, as a mailbox did before
// it could hold any.
func TestAPolicyCanLandEverySendersMessagesInInbox(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	if err := b.SetPolicy(t.Context(), client.Policy{Unknown: client.LandInbox, Server: client.AdmitAll}); err != nil {
		t.Fatal(err)
	}
	send(t, a, b, "to an open mailbox")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Held) != 0 {
		t.Fatalf("%+v", res)
	}
}

// Under "server allowed" the server refuses every sender the mailbox has not
// allowed, and learns who those are: a send to a new address tells it
// before the message is posted, so the reply is not refused.
func TestUnderServerAllowedTheServerRefusesTheRest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, stranger := e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
	if err := b.SetPolicy(t.Context(), client.Policy{Unknown: client.LandHeld, Server: client.AdmitAllowed}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/acl") || r.Method == http.MethodPost {
			calls = append(calls, r.Method)
		}
		next.ServeHTTP(w, r)
	})
	send(t, b, a, "hello")
	if strings.Join(calls, " ") != "PUT POST" {
		t.Fatalf("the calls of a send to a new address: %v", calls)
	}
	acl := e.srv.ACL(b.Address())
	if acl == nil || acl.Allow.Any || !acl.Allow.Has(a.Address()) || acl.Allow.Len() != 1 {
		t.Fatalf("the server holds %+v", acl)
	}
	send(t, a, b, "the reply")
	if _, err := stranger.Send(ctx, b.Address(), "refused"); client.Code(err) != protocol.CodeSenderNotAllowed {
		t.Fatalf("a stranger's send: %v", err)
	}
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Held) != 0 {
		t.Fatalf("%+v", res)
	}
	// A second send to the same address sends no ACL: nothing changed.
	calls = nil
	send(t, b, a, "again")
	if strings.Join(calls, " ") != "POST" {
		t.Fatalf("the calls of a send to an allowed address: %v", calls)
	}
	// Back to "server all": the server lets everyone in again.
	if err := b.SetPolicy(t.Context(), client.DefaultPolicy()); err != nil {
		t.Fatal(err)
	}
	if err := b.SyncACL(ctx); err != nil {
		t.Fatal(err)
	}
	if acl := e.srv.ACL(b.Address()); !acl.Allow.Any || acl.Deny.Len() != 0 {
		t.Fatalf("the server holds %+v", acl)
	}
}

// An ACL replaces whatever the server holds, whoever sent it: a mailbox whose
// record of what its server took is gone, as on a second machine of the
// mailbox, sends the ACL its files say in one PUT, with no If-Match and no read
// first, and keeps the server's answer.
func TestAnACLReplacesWhatTheServerHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	if _, err := b.Block(ctx, a.Address()); err != nil {
		t.Fatal(err)
	}
	b.SetRecord(t, "acl.json", nil) // as on a second machine
	var calls []string
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if strings.HasSuffix(r.URL.Path, "/acl") {
			calls = append(calls, r.Method+" If-Match:"+r.Header.Get("If-Match"))
		}
		next.ServeHTTP(w, r)
	})
	c := e.heldMailbox()
	if _, err := b.Block(ctx, c.Address()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ", ") != "PUT If-Match:" {
		t.Fatalf("the calls of the ACL: %q", calls)
	}
	acl := e.srv.ACL(b.Address())
	if acl.Deny.Len() != 2 || !acl.Deny.Has(a.Address()) || !acl.Deny.Has(c.Address()) {
		t.Fatalf("the server holds %+v", acl)
	}
	var took protocol.ACLDocument
	data, _ := stateOf(b, "acl.json")
	if err := protocol.Unmarshal(data, &took); err != nil || len(took.Deny) != 2 {
		t.Fatalf("the ACL kept: %s, %v", data, err)
	}
}

// An ACL another writer put at the server, another machine of the mailbox or
// whoever holds its pass, is read at the mailbox's next renewal and replaced by
// the mailbox's own, which Receive reports; until then nothing asks the server
// about it, neither a drain nor a send. A renewal that finds the mailbox's own
// reads it and sends nothing (dromas-dev/dromas-client#360).
func TestARenewalReplacesAnACLAnotherWriterPut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	if _, err := b.Block(ctx, a.Address()); err != nil {
		t.Fatal(err)
	}
	own := e.srv.ACL(b.Address())
	var calls []string
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.URL.Path == "/v1/"+b.Address().String()+"/acl" {
			calls = append(calls, r.Method)
		}
		next.ServeHTTP(w, r)
	})
	took := func() string {
		t.Helper()
		data, _ := stateOf(b, "acl.json")
		return string(data)
	}
	kept := took()

	e.clock.Add(25 * time.Hour)
	if res := receive(t, b); res.Renewed == nil || res.ACLReplaced || res.ACLErr != nil || strings.Join(calls, " ") != "GET" {
		t.Fatalf("a renewal that finds the mailbox's own ACL: %+v, calls %v", res, calls)
	}

	// Another writer pauses the mailbox. A drain and a send ask nothing of it.
	pause := &protocol.ACL{Deny: protocol.ACLList{Any: true}}
	e.srv.PutACL(b.Address(), pause)
	calls = nil
	if res := receive(t, b); res.Renewed != nil || res.ACLReplaced {
		t.Fatalf("a drain with no renewal due: %+v", res)
	}
	send(t, b, a, "a send while paused")
	if len(calls) != 0 || !e.srv.ACL(b.Address()).SameLists(pause) {
		t.Fatalf("a drain and a send called %v, and the server holds %+v", calls, e.srv.ACL(b.Address()))
	}

	// The next renewal reads it, and puts the mailbox's own in its place.
	e.clock.Add(25 * time.Hour)
	res := receive(t, b)
	if res.Renewed == nil || !res.ACLReplaced || res.ACLErr != nil || strings.Join(calls, " ") != "GET PUT" {
		t.Fatalf("the renewal after another writer's ACL: %+v, calls %v", res, calls)
	}
	if !e.srv.ACL(b.Address()).SameLists(own) || took() != kept {
		t.Fatalf("the server holds %+v, the mailbox keeps %s; want %+v, %s", e.srv.ACL(b.Address()), took(), own, kept)
	}
}

// Where the ACL a mailbox's files say is not the one its server last took, the
// error of the sync that did not send it and status say what changed: none
// taken from the mailbox, or the mailbox's own lists changed.
func TestWhatChangedInAnACLNotTakenIsSaid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct{ name, what string }{
		{"none taken", "this mailbox's server holds no ACL this mailbox sent"},
		{"its lists changed", "this mailbox's lists have changed since its server last took its ACL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			b, d, x := e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
			var refusing atomic.Bool
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if refusing.Load() && r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/acl") {
					forge.Refuse(w, protocol.CodeUnavailable)
					return
				}
				next.ServeHTTP(w, r)
			})
			mailbox := b
			if c.name == "its lists changed" {
				if _, err := b.Block(ctx, d.Address()); err != nil {
					t.Fatal(err)
				}
			}
			refusing.Store(true)
			_, err := mailbox.Block(ctx, x.Address())
			st, serr := mailbox.Status(ctx)
			if err == nil || !strings.Contains(err.Error(), c.what) || serr != nil || !strings.Contains(strings.Join(st.Attention, "\n"), c.what) {
				t.Fatalf("the sync not taken: %v; status %q, %v; want each to say %q", err, st.Attention, serr, c.what)
			}
		})
	}
}

// A server that answers mailbox_changed to an ACL (the mailbox changed while it
// handled the request) is sent it again, as it is: the write is not lost to
// a race.
func TestAnACLIsSentAgainWhenTheMailboxChanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	var once sync.Once
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		raced := false
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/acl") {
			once.Do(func() { raced = true })
		}
		if raced {
			fakeserver.WriteJSON(w, protocol.CodeMailboxChanged.Status(), protocol.Problem{Type: protocol.CodeMailboxChanged.TypeURI(), Code: protocol.CodeMailboxChanged, Status: int64(protocol.CodeMailboxChanged.Status())})
			return
		}
		next.ServeHTTP(w, r)
	})
	if _, err := b.Block(ctx, a.Address()); err != nil {
		t.Fatalf("block when the mailbox changed once: %v", err)
	}
	if acl := e.srv.ACL(b.Address()); !acl.Deny.Has(a.Address()) {
		t.Fatalf("the server holds %+v", acl)
	}
}

// A list that cannot be read stops the drain before anything is acked, and
// status says so: it is never read as the default.
func TestAListThatCannotBeReadStopsTheDrain(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "waits")
	for name, text := range map[string]string{"allowed": "not an address\n", "blocked": strings.ToUpper(a.Address().String()) + "\n"} {
		if err := os.WriteFile(filepath.Join(b.Dir(), name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		var refused *client.RefusedError
		if _, err := b.Receive(context.Background()); !errors.As(err, &refused) || e.srv.Count(b.Address()) != 1 {
			t.Fatalf("a drain with %s %q: %v, %d left", name, text, err, e.srv.Count(b.Address()))
		}
		if st, err := b.Status(t.Context()); err != nil || !strings.Contains(strings.Join(st.Attention, "\n"), "neither sends nor receives") {
			t.Fatalf("status with %s %q: %+v, %v", name, text, st, err)
		}
		if err := os.Remove(filepath.Join(b.Dir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	if res := receive(t, b); len(res.Held) != 1 {
		t.Fatalf("%+v", res)
	}
}

// RebuildAllowed adds back what aliases and sent/ show, for an allowed file
// that was lost, and keeps what the file lists.
func TestRebuildAllowed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b, c, d := e.heldMailbox(), e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
	send(t, b, a, "to a")
	if err := b.SetAlias(t.Context(), "carol", c.Address()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir(), "allowed"), []byte(d.Address().String()), 0o600); err != nil { // no line end, as a person may write it
		t.Fatal(err)
	}
	n, err := b.RebuildAllowed(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("rebuild: %d, %v", n, err)
	}
	if allowed, err := b.Allowed(t.Context()); err != nil || len(allowed) != 3 {
		t.Fatalf("allowed: %v, %v", allowed, err)
	}
	if n, err := b.RebuildAllowed(t.Context()); err != nil || n != 0 {
		t.Fatalf("a second rebuild: %d, %v", n, err)
	}
}

// A sender the mailbox blocks is never allowed: allow is refused, naming the
// block, and its held messages stay where they are (#179).
func TestAllowingABlockedSenderDoesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "held, then its sender blocked by hand")
	if res := receive(t, b); len(res.Held) != 1 {
		t.Fatalf("held %d", len(res.Held))
	}
	if err := os.WriteFile(filepath.Join(b.Dir(), "blocked"), []byte(a.Address().String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := b.Allow(ctx, a.Address())
	inbox, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "inbox", "*.json"))
	held, _ := filepath.Glob(filepath.Join(b.Dir(), "mail", "held", "*.json"))
	allowed, _ := b.Allowed(t.Context())
	if err == nil || !strings.Contains(err.Error(), "is blocked") || len(moved) != 0 || len(inbox) != 0 || len(held) != 1 || slices.Contains(allowed, a.Address()) {
		t.Fatalf("allow of a blocked sender: %v, moved %d, inbox %d, held %d, allowed %v", err, len(moved), len(inbox), len(held), allowed)
	}
}

// A send waits for the mailbox's ACL only when the one its server last took would
// refuse the reply (#180): with none taken the server refuses no sender, so
// the send posts and says the ACL was not taken; with one taken under server
// allowed, a send to a peer it does not allow waits.
func TestASendWaitsOnTheACLOnlyWhenTheReplyWouldBeRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, c, d := e.heldMailbox(), e.heldMailbox(), e.heldMailbox(), e.heldMailbox()
	refuseACL := func() {
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/acl") {
				code := protocol.CodeTooLarge
				fakeserver.WriteJSON(w, code.Status(), protocol.Problem{Type: code.TypeURI(), Code: code, Status: int64(code.Status())})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	refuseACL()
	if _, err := b.Block(ctx, c.Address()); err == nil {
		t.Fatal("the server took the ACL it refuses")
	}
	s, err := b.Send(ctx, a.Address(), "to a peer, with no ACL taken")
	if err != nil || s.ACLErr == nil || e.srv.Count(a.Address()) != 1 {
		t.Fatalf("a send with the ACL refused and none taken: %+v, %v; posted %d", s, err, e.srv.Count(a.Address()))
	}
	e.srv.SetMiddle(nil)
	if err := b.SetPolicy(t.Context(), client.Policy{Unknown: client.LandHeld, Server: client.AdmitAllowed}); err != nil {
		t.Fatal(err)
	}
	if err := b.SyncACL(ctx); err != nil || !e.srv.ACL(b.Address()).Allow.Has(a.Address()) {
		t.Fatalf("the ACL under server allowed: %v", err)
	}
	refuseACL()
	if _, err := b.Send(ctx, d.Address(), "to a peer the ACL taken does not allow"); err == nil || e.srv.Count(d.Address()) != 0 {
		t.Fatalf("a send whose reply the ACL taken would refuse: %v; posted %d", err, e.srv.Count(d.Address()))
	}
}

// A line of the mailbox's address lists that starts with "#" is a comment, as in
// its policy file: it stops no drain and no send (#181). One after an address
// on its line is still no address.
func TestACommentInTheAddressListsStopsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.heldMailbox(), e.heldMailbox()
	send(t, a, b, "waiting")
	if err := os.WriteFile(filepath.Join(b.Dir(), "blocked"), []byte("# senders of spam, 2026-10\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Receive(ctx); err != nil {
		t.Fatalf("recv with a comment in blocked: %v", err)
	}
	if _, err := b.Send(ctx, a.Address(), "an answer"); err != nil {
		t.Fatalf("send with a comment in blocked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir(), "blocked"), []byte(a.Address().String()+" # spam\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Blocked(t.Context()); err == nil {
		t.Fatal("an address with a comment after it was read as an address")
	}
}

// A mailbox restored from its identity alone, with none of its allowed and
// blocked files, is a failure, said once: Init reports it, and the first
// renewal recv makes replaces the ACL its server holds with the open one the
// empty lists make, so the sender it blocked is no longer denied there (#832).
// Control: restored with its blocked file, it reports nothing, and the block
// stays.
func TestARestoreWithoutItsListsIsSaidAndReplacesItsServersACL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, withLists := range []bool{false, true} {
		t.Run(fmt.Sprintf("with its lists %v", withLists), func(t *testing.T) {
			e := newEnv(t)
			b, d := e.mailbox(), e.mailbox()
			if _, err := b.Block(ctx, d.Address()); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			files := []string{"identity"}
			if withLists {
				files = append(files, "blocked")
			}
			for _, name := range files {
				data, err := os.ReadFile(filepath.Join(b.Dir(), name))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			e.clock.Add(time.Minute)
			r, err := client.Init(ctx, dir, e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if r.RestoredWithoutLists() == withLists {
				t.Fatalf("restored without its lists: %v, with them %v", r.RestoredWithoutLists(), withLists)
			}
			e.clock.Add(25 * time.Hour)
			res, err := r.Receive(ctx)
			if err != nil || res.Renewed == nil {
				t.Fatalf("the first recv a day on: %+v, %v", res, err)
			}
			denied := e.srv.ACL(b.Address()) != nil && e.srv.ACL(b.Address()).Deny.Has(d.Address())
			if denied != withLists || res.ACLReplaced == withLists {
				t.Fatalf("the server still denies the blocked sender: %v, replaced %v; with its lists %v", denied, res.ACLReplaced, withLists)
			}
		})
	}
}
