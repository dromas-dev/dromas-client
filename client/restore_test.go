package client_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// mailCommands are the commands of lockingCommands that receive, send, renew or
// hand over a message, which a state that does not match prekeys/ stops; every
// other command there runs on it.
var mailCommands = []string{"Receive", "Hold", "Await", "Follow", "Rekey", "RenewAndRevoke", "RenewUnderCertificate", "SendWith", "SendJSON", "Flush", "Allow"}

// notMail are the commands of lockingCommands that touch no message, which run
// on a state that does not match prekeys/.
var notMail = []string{"Adopt", "Leave", "Resolve", "RevokeEarlier", "Block", "SyncACL", "Ask", "KeepRevocation", "Publish", "SetAlias", "RemoveAlias", "RemoveRefused", "ClearFailed", "SetPolicy", "RebuildAllowed"}

// signals are the states that do not match prekeys/, each made from a mailbox b
// that has delivered a message from a, with a message of a's that the server
// kept a copy of: a key the state names nowhere, as after the state is lost.
var signals = []struct {
	name string
	make func(t *testing.T, e *env, a, b *client.Mailbox) *client.Mailbox
}{
	{"a key the state names nowhere", func(t *testing.T, e *env, a, b *client.Mailbox) *client.Mailbox {
		send(t, a, b, "delivered before the state was lost")
		if res := receive(t, b); len(res.Delivered) != 1 {
			t.Fatalf("the first delivery: %+v", res)
		}
		return lostUnacknowledged(t, e, b)
	}},
}

// Each signal of a state that does not match prekeys/ stops every command that
// receives, sends, renews or hands over a message, init among them, before it
// makes any call: nothing is listed, fetched or acked, and nothing is renewed.
// It is refused as StateNotTheKeys, which status says too, and every other
// command runs. Each list is held to lockingCommands, which
// TestEveryLockingCommandIsTriedFinished holds to the code. A mailbox whose
// state matches its prekeys/ is the control.
func TestEachSignalStopsEveryCommandThatTouchesMessages(t *testing.T) {
	t.Parallel()
	refused, run := lockingCommands(context.Background(), nil, nil, nil, "")
	for _, c := range append(refused, run...) {
		if !slices.Contains(mailCommands, c.name) && !slices.Contains(notMail, c.name) {
			t.Errorf("%s takes the lock and is in neither mailCommands nor notMail", c.name)
		}
	}
	for _, s := range append(signals, struct {
		name string
		make func(t *testing.T, e *env, a, b *client.Mailbox) *client.Mailbox
	}{"control: a state that matches", func(_ *testing.T, _ *env, _, b *client.Mailbox) *client.Mailbox { return b }}) {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			x := s.make(t, e, a, b)
			control := strings.HasPrefix(s.name, "control")
			send(t, a, x, "waits on the server")
			var calls atomic.Int32
			watch(e, func(http.ResponseWriter, *http.Request) bool {
				calls.Add(1)
				return false
			})
			opt := e.opts()
			if _, err := client.Init(ctx, x.Dir(), opt); client.NotTheKeys(err) == control || !control && calls.Load() != 0 {
				t.Errorf("init: %v, with %d calls", err, calls.Load())
			}
			st, err := x.Status(ctx)
			if says := err == nil && slices.ContainsFunc(st.Attention, func(w string) bool { return strings.Contains(w, "dromas init --restore") }); says == control {
				t.Errorf("status: %q, %v", st.Attention, err)
			}
			rec, err := x.Revocation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			refused, run := lockingCommands(ctx, x, a, rec, "key")
			all := append(refused, run...)
			slices.SortStableFunc(all, func(p, q command) int {
				// The mail commands first, then the rest, Publish and
				// KeepRevocation last: they finish the mailbox.
				rank := func(c command) int {
					switch {
					case slices.Contains(mailCommands, c.name):
						return 0
					case c.name == "Publish" || c.name == "KeepRevocation":
						return 2
					}
					return 1
				}
				return rank(p) - rank(q)
			})
			for i, c := range all {
				if i > 0 && slices.Contains(mailCommands, all[i-1].name) && !slices.Contains(mailCommands, c.name) {
					if control {
						break
					}
					if n := e.srv.Count(x.Address()); n != 1 {
						t.Errorf("the server holds %d messages, want the one that waits: nothing was acked", n)
					}
				}
				calls.Store(0)
				e.clock.Add(2 * time.Second)
				err := c.f()
				switch mail := slices.Contains(mailCommands, c.name); {
				case control && client.NotTheKeys(err):
					t.Errorf("%s on a state that matches: %v", c.name, err)
				case control:
				case mail && (!client.NotTheKeys(err) || calls.Load() != 0):
					t.Errorf("%s: %v, with %d calls to the server", c.name, err, calls.Load())
				case !mail && client.NotTheKeys(err):
					t.Errorf("%s touches no message, and was refused: %v", c.name, err)
				}
			}
		})
	}
}

// dromas init --restore on a mailbox whose state matches its prekeys/ changes
// nothing, so it may run at every start: it says it acknowledged nothing, and a
// message sent since is delivered.
func TestInitRestoreOnAMailboxThatMatchesChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "delivered before")
	receive(t, b)
	before, err := client.RecordsAt(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	files := snapshot(b.Dir())
	opt := e.opts()
	opt.Restore = true
	r, err := client.Init(ctx, b.Dir(), opt)
	if err != nil || r.Restored() == nil || r.Restored().Acknowledged() {
		t.Fatalf("init --restore: %+v, %v", r.Restored(), err)
	}
	after, err := client.RecordsAt(b.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if c := changes(files, snapshot(b.Dir())); c != "nothing" || len(after) != len(before) {
		t.Fatalf("init --restore changed %s, %d records to %d", c, len(before), len(after))
	}
	for name, v := range before {
		if string(after[name]) != string(v) {
			t.Errorf("init --restore changed the record %s", name)
		}
	}
	send(t, a, b, "sent since")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message sent since: %+v", res)
	}
}

// A mailbox restored from its identity alone holds no prekey, so it needs no
// acknowledgement: its init restores it, adopts nothing, and a message sent
// since is delivered.
func TestARestoreFromTheIdentityAloneNeedsNoAcknowledgement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "delivered before")
	receive(t, b)
	identityAlone(t, b.Dir())
	e.clock.Add(2 * time.Second)
	x, err := client.Init(ctx, b.Dir(), e.opts())
	if err != nil {
		t.Fatalf("init from the identity alone: %v", err)
	}
	landAll(t, x)
	if has(x, "seen-floor") || len(client.AdoptedKeys(t, x)) != 0 {
		t.Fatal("a restore from the identity alone adopted a key or raised a floor")
	}
	send(t, a, x, "sent since")
	if res := receive(t, x); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message sent since: %+v", res)
	}
}

// Options.Restore is Init's alone (#799 item 2): a Mailbox opened with it
// acknowledges nothing, and the Mailbox Init returns under it is stopped, as
// any other is, by a key that appears in prekeys/ after Init, as on a volume
// mounted late.
func TestRestoreIsInitsAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("Open", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		a, b := e.mailbox(), e.mailbox()
		send(t, a, b, "delivered before the state was lost")
		receive(t, b)
		if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
			t.Fatal(err)
		}
		opt := e.opts()
		opt.Restore = true
		x, err := client.Open(b.Dir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		landAll(t, x)
		if _, err := x.Receive(ctx); !client.NotTheKeys(err) || len(client.AdoptedKeys(t, x)) != 0 {
			t.Fatalf("Receive on a Mailbox opened with Restore, on a lost state: %v, adopted %v", err, client.AdoptedKeys(t, x))
		}
	})
	t.Run("Init", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		a, b := e.mailbox(), e.mailbox()
		send(t, a, b, "delivered")
		receive(t, b)
		opt := e.opts()
		opt.Restore = true
		y, err := client.Init(ctx, b.Dir(), opt) // init --restore at every start: on a state that matches it changes nothing
		if err != nil || y.Restored() == nil || y.Restored().Acknowledged() {
			t.Fatalf("init --restore on a state that matches: %+v, %v", y.Restored(), err)
		}
		landAll(t, y)
		key, err := os.ReadFile(prekeyFile(y, numberOf(t, y), ".agekey"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(prekeyFile(y, numberOf(t, y)-1, ".agekey"), key, 0o600); err != nil {
			t.Fatal(err)
		}
		if st, err := y.Status(ctx); err != nil || !slices.ContainsFunc(st.Attention, func(w string) bool { return strings.Contains(w, "dromas init --restore") }) {
			t.Fatalf("status does not name the key: %+v, %v", st, err)
		}
		e.clock.Add(2 * time.Second)
		if _, err := y.Receive(ctx); !client.NotTheKeys(err) || len(client.AdoptedKeys(t, y)) != 0 {
			t.Fatalf("Receive on the Mailbox Init returned, after a key appeared: %v, adopted %v", err, client.AdoptedKeys(t, y))
		}
	})
}

// An Init under Restore whose acknowledgement committed and which then failed,
// on a server that answers 503, says what the acknowledgement did in its error
// (RestoredBy, #799 item 3), since the next Init under Restore finds the state
// matching and nothing to acknowledge.
func TestAnInitThatFailsAfterItsAcknowledgementSaysWhatItDid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "delivered once")
	receive(t, b)
	if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
		t.Fatal(err)
	}
	e.srv.SetMiddle(func(w http.ResponseWriter, _ *http.Request, _ http.Handler) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	opt := e.opts()
	opt.Restore = true
	_, err := client.Init(ctx, b.Dir(), opt)
	if r := client.RestoredBy(err); err == nil || r == nil || r.Adopted == 0 {
		t.Fatalf("init --restore with its server down: %+v, %v", r, err)
	}
	var api *client.APIError
	if !errors.As(err, &api) || api.Status != http.StatusServiceUnavailable {
		t.Fatalf("the error does not carry the server's answer: %v", err)
	}
	e.srv.SetMiddle(nil)
	y, err := client.Init(ctx, b.Dir(), opt)
	if err != nil || y.Restored() == nil || y.Restored().Acknowledged() || client.RestoredBy(err) != nil {
		t.Fatalf("init --restore again: %+v, %v", y.Restored(), err)
	}
}

// Follow under FollowOptions.Owed hands over no owed message on a state that
// does not match prekeys/: it is refused before the handover, as the drain
// after it is (#799 item 4).
func TestFollowHandsOverNoOwedMessageOnAStateThatDoesNotMatch(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "owed")
	boom := errors.New("boom")
	if err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			return boom
		}
		return nil
	}); !errors.Is(err, boom) {
		t.Fatalf("the first Follow: %v", err)
	}
	if st, err := b.Status(context.Background()); err != nil || st.Owed != 1 {
		t.Fatalf("owed: %+v, %v", st, err)
	}
	key, err := os.ReadFile(prekeyFile(b, numberOf(t, b), ".agekey"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prekeyFile(b, numberOf(t, b)-1, ".agekey"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	handed := 0
	err = b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			handed++
		}
		return nil
	})
	if !client.NotTheKeys(err) || handed != 0 {
		t.Errorf("Follow on a state that does not match: %v, having handed over %d", err, handed)
	}
}
