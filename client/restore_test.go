package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// generationFile is the mailbox's prekeys/generation.
func generationFile(b *client.Mailbox) string { return filepath.Join(b.Path("prekeys"), "generation") }

// generationOf is the count prekeys/generation keeps, and -1 where it keeps none.
func generationOf(t *testing.T, b *client.Mailbox) int {
	t.Helper()
	raw, err := os.ReadFile(generationFile(b))
	if os.IsNotExist(err) {
		return -1
	}
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSuffix(string(raw), "\n"))
	if err != nil {
		t.Fatalf("prekeys/generation keeps %q", raw)
	}
	return n
}

// stateGeneration is the count the state keeps, and 0 where it keeps none.
func stateGeneration(t *testing.T, b *client.Mailbox) int {
	t.Helper()
	raw, ok := b.Record(t, "generation")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatalf("the state's generation %q", raw)
	}
	return n
}

// mailCommands are the commands of lockingCommands that receive, send, renew or
// hand over a message, which a state that does not match prekeys/ stops; every
// other command there runs on it.
var mailCommands = []string{"Receive", "Hold", "Await", "Follow", "Rekey", "RenewAndRevoke", "RenewUnderCertificate", "SendWith", "SendJSON", "Flush", "Allow"}

// notMail are the commands of lockingCommands that touch no message, which run
// on a state that does not match prekeys/.
var notMail = []string{"Adopt", "Leave", "Resolve", "RevokeEarlier", "Block", "SyncACL", "Ask", "KeepRevocation", "Publish", "SetAlias", "RemoveAlias", "RemoveRefused", "ClearFailed", "SetPolicy", "RebuildAllowed"}

// signals are the two states that do not match prekeys/, each made from a
// mailbox b that has delivered a message from a, with a message of a's that
// the server kept a copy of: a key the state names nowhere, as after the state
// is lost, and a state put back from a copy older than a drain that delivered.
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
	{"a state older than a drain that delivered", func(t *testing.T, e *env, a, b *client.Mailbox) *client.Mailbox {
		path := filepath.Join(b.Dir(), "state")
		old, err := os.ReadFile(path) //nolint:gosec // a test mailbox's own file
		if err != nil {
			t.Fatal(err)
		}
		send(t, a, b, "delivered after the copy was taken")
		if res := receive(t, b); len(res.Delivered) != 1 {
			t.Fatalf("the delivery after the copy: %+v", res)
		}
		if err := os.WriteFile(path, old, 0o600); err != nil {
			t.Fatal(err)
		}
		x, err := client.Open(b.Dir(), e.opts())
		if err != nil {
			t.Fatal(err)
		}
		return x
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

// A state put back from a copy older than a drain that delivered, with every
// key named, is behind prekeys/generation: recv stops before it lists
// anything, dromas init --restore acknowledges it and owes the floor, and the
// copy the server kept of the message the drain delivered is refused as stale,
// with its signer (PROTOCOL.md §12.3 check 8, §15.9).
func TestAStateOlderThanADrainIsStoppedThenAcknowledgedAndTheFloorHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	path := filepath.Join(b.Dir(), "state")
	old, err := os.ReadFile(path) //nolint:gosec // a test mailbox's own file
	if err != nil {
		t.Fatal(err)
	}
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "delivered after the copy was taken")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the delivery after the copy: %+v", res)
	}
	if generationOf(t, b) != stateGeneration(t, b) || stateGeneration(t, b) == 0 {
		t.Fatalf("the drain counted %d in prekeys/generation and %d in the state", generationOf(t, b), stateGeneration(t, b))
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(2 * time.Second)
	id := sha256.Sum256(*sent)
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // the server kept its copy
	if _, err := x.Receive(ctx); !client.NotTheKeys(err) || e.srv.Count(x.Address()) != 1 {
		t.Fatalf("recv on the older copy: %v", err)
	}
	restoreAcknowledged(t, e, x, 0, true)
	if generationOf(t, x) != stateGeneration(t, x) {
		t.Fatalf("after the acknowledgement: %d in prekeys/generation and %d in the state", generationOf(t, x), stateGeneration(t, x))
	}
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refusedBeforeRestore(t, x, res, a, 1)
	e.clock.Add(protocol.MaxSkew)
	send(t, a, x, "sent once the floor's edge has passed")
	if res := receive(t, x); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message past the floor's edge: %+v", res)
	}
}

// The floor after a state is put back from an older copy is dated by a pass
// signed after the acknowledgement, never by the pass the copy kept, which may
// be older than the last message the lost replay set took: a message the lost
// set took, dated 59 minutes ahead of the reader's clock under the kept pass,
// as a server that keeps a sender's post whatever its clock can store, is
// refused as stale at the next drain, a minute after the acknowledgement.
func TestARollbackIsDatedByAPassSignedSince(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	if res := receive(t, b); len(res.Delivered) != 0 { // signs and keeps a pass
		t.Fatalf("the first recv: %+v", res)
	}
	path := filepath.Join(b.Dir(), "state")
	old, err := os.ReadFile(path) //nolint:gosec // a test mailbox's own file
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(5 * time.Minute)
	line := forge.Line(t, a.Identity().Document(), a.Address(), b.Address(), "pay invoice 13 once", e.clock.Now().Add(59*time.Minute))
	ct := forge.Encrypt(t, e.srv, b.Address(), line, forge.Sign(t, worker(t, a), line, b.Address()))
	sum := sha256.Sum256(ct)
	id := hex.EncodeToString(sum[:])
	e.srv.Inject(b.Address(), id, ct)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the delivery under the kept pass: %+v", res)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Minute)
	restoreAcknowledged(t, e, b, 0, true)
	e.srv.Inject(b.Address(), id, ct) // the server kept its copy
	res, err := b.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Delivered) != 0 || res.RefusedBeforeRestore() != 1 {
		t.Fatalf("the server's copy a minute after the acknowledgement: %+v", res)
	}
}

// dromas init --restore on a mailbox whose state matches its prekeys/ changes
// nothing, so it may run at every start: it says it acknowledged nothing,
// owes no floor, drops no pass, and a message sent since is delivered.
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
	if c := changes(files, snapshot(b.Dir())); c != "nothing" || len(after) != len(before) || has(b, "floor-owed") {
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

// A drain killed between the count it keeps in its state, in the transaction
// that records the message seen, and the count it writes to prekeys/generation
// leaves the file behind the state, never ahead: the next command raises no
// alarm, writes the file again, and acks the message, which inbox/ keeps
// already, as one handled. A command killed as it writes a missing file again
// raises no alarm either, and delivers the message.
func TestACrashBetweenTheCountAndItsFileRaisesNoAlarm(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		step, file string
		delivered  int
	}{
		{"delivery seen", "", 0},
		{"generation synced", "removed", 1},
	} {
		t.Run(c.step, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "waits for a drain that dies")
			if c.file == "removed" {
				if err := os.Remove(generationFile(b)); err != nil {
					t.Fatal(err)
				}
			}
			crash(t, e, b, "receive", c.step, protocol.Address{})
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			if c.step == "delivery seen" && generationOf(t, x) >= stateGeneration(t, x) {
				t.Fatalf("killed at %s: %d in prekeys/generation and %d in the state, want the file behind", c.step, generationOf(t, x), stateGeneration(t, x))
			}
			res, err := x.Receive(context.Background())
			if err != nil || len(res.Delivered) != c.delivered {
				t.Fatalf("the drain after the crash: %+v, %v", res, err)
			}
			if kept, err := filepath.Glob(filepath.Join(x.Path("inbox"), "*.json")); err != nil || len(kept) != 1 {
				t.Fatalf("inbox/ after the drain: %v, %v", kept, err)
			}
			if generationOf(t, x) != stateGeneration(t, x) {
				t.Fatalf("after the drain: %d in prekeys/generation and %d in the state", generationOf(t, x), stateGeneration(t, x))
			}
		})
	}
}

// A mailbox with no prekeys/generation, as one made before the file was, has
// counted nothing there: it reads forward, owing nothing, and writes the file
// with the count its state keeps. So does a state that keeps no count.
func TestAMissingCountReadsForward(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "delivered before")
	receive(t, b)
	kept := stateGeneration(t, b)
	if err := os.Remove(generationFile(b)); err != nil {
		t.Fatal(err)
	}
	send(t, a, b, "delivered with no count on disk")
	if res, err := b.Receive(ctx); err != nil || len(res.Delivered) != 1 || has(b, "floor-owed") {
		t.Fatalf("a drain with no prekeys/generation: %+v, %v", res, err)
	}
	if generationOf(t, b) != kept+1 || stateGeneration(t, b) != kept+1 {
		t.Fatalf("prekeys/generation %d and the state %d, want both %d", generationOf(t, b), stateGeneration(t, b), kept+1)
	}
	b.SetRecord(t, "generation", nil)
	if err := os.Remove(generationFile(b)); err != nil {
		t.Fatal(err)
	}
	send(t, a, b, "delivered with no count anywhere")
	if res, err := b.Receive(ctx); err != nil || len(res.Delivered) != 1 || has(b, "floor-owed") {
		t.Fatalf("a drain with no count anywhere: %+v, %v", res, err)
	}
}

// A mailbox restored from its identity alone holds no prekey and no count, so
// it needs no acknowledgement: its init restores it, it owes no floor, and a
// message sent since is delivered.
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
	if has(x, "floor-owed") || has(x, "seen-floor") || has(x, "seen-began") || len(client.AdoptedKeys(t, x)) != 0 {
		t.Fatal("a restore from the identity alone owes a floor")
	}
	send(t, a, x, "sent since")
	if res := receive(t, x); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message sent since: %+v", res)
	}
}

// A copy of the state taken inside a drain, as a backup that copies the base
// directory while recv runs takes one, is behind prekeys/generation once the
// drain accepts a message after it, since each message accepted is counted:
// put back with prekeys/ kept, it is stopped before anything is listed, and
// status names it, so a server that kept its copies replays nothing (#798). A
// copy taken once the drain accepted its last message matches, and the
// server's copies are acked as handled, delivered to nobody.
func TestACopyOfTheStateTakenInsideADrainIsStopped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		at      int // the ack the copy is taken at
		stopped bool
	}{
		{1, true},
		{2, false},
	} {
		t.Run(strconv.Itoa(c.at), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			path := filepath.Join(b.Dir(), "state")
			mine := "/v1/" + b.Address().String() + "/messages"
			var mu sync.Mutex
			var cts [][]byte
			var copied []byte
			acks := 0
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				mu.Lock()
				switch {
				case r.Method == http.MethodPost && r.URL.Path == mine:
					body, _ := io.ReadAll(r.Body)
					r.Body = io.NopCloser(bytes.NewReader(body))
					cts = append(cts, body)
				case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, mine+"/"):
					if acks++; acks == c.at { // a backup of the state taken as the drain acks
						var err error
						if copied, err = os.ReadFile(path); err != nil { //nolint:gosec // a test mailbox's own file
							t.Error(err)
						}
					}
				}
				mu.Unlock()
				next.ServeHTTP(w, r)
			})
			send(t, a, b, "first")
			send(t, a, b, "second: pay invoice 13 once")
			before := stateGeneration(t, b)
			if res := receive(t, b); len(res.Delivered) != 2 {
				t.Fatalf("the drain: %+v", res)
			}
			e.srv.SetMiddle(nil)
			if got := stateGeneration(t, b); got != before+2 || generationOf(t, b) != got {
				t.Fatalf("a drain that accepted two: %d in the state and %d in prekeys/generation, want both %d", got, generationOf(t, b), before+2)
			}
			if err := os.WriteFile(path, copied, 0o600); err != nil {
				t.Fatal(err)
			}
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			e.clock.Add(2 * time.Second)
			for _, ct := range cts { // the server kept its copies
				sum := sha256.Sum256(ct)
				e.srv.Inject(x.Address(), hex.EncodeToString(sum[:]), ct)
			}
			st, err := x.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			says := slices.ContainsFunc(st.Attention, func(w string) bool { return strings.Contains(w, "dromas init --restore") })
			res, err := x.Receive(ctx)
			if c.stopped {
				if !client.NotTheKeys(err) || !says {
					t.Fatalf("a copy taken at ack %d: status names it %v; recv %+v, %v", c.at, says, res, err)
				}
				return
			}
			if err != nil || says || len(res.Delivered) != 0 || len(res.Refused) != 0 {
				t.Fatalf("a copy taken at the last ack: status names it %v; recv %+v, %v", says, res, err)
			}
		})
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
		if _, err := x.Receive(ctx); !client.NotTheKeys(err) || has(x, "floor-owed") || len(client.AdoptedKeys(t, x)) != 0 {
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

// The count is raised in the transaction that records a message seen, never
// before it: a copy of the state taken once the drain's last message is
// written to inbox/, and before it is recorded seen, is behind
// prekeys/generation when put back, so that message is never delivered again.
// Not parallel: SetStep is the package's.
func TestACopyTakenBeforeAMessageIsRecordedSeenIsBehind(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	path := filepath.Join(b.Dir(), "state")
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "first")
	first := *sent
	send(t, a, b, "second: pay invoice 13 once")
	e.srv.SetMiddle(nil)
	var copied []byte
	written := 0
	client.SetStep(func(name string) {
		if name != "delivery written" {
			return
		}
		if written++; written == 2 { // the last message written, not yet seen
			var err error
			if copied, err = os.ReadFile(path); err != nil { //nolint:gosec // a test mailbox's own file
				t.Error(err)
			}
		}
	})
	t.Cleanup(func() { client.SetStep(nil) })
	if res := receive(t, b); len(res.Delivered) != 2 || copied == nil {
		t.Fatalf("the drain: %+v, copied %v", res, copied != nil)
	}
	client.SetStep(nil)
	if err := os.WriteFile(path, copied, 0o600); err != nil {
		t.Fatal(err)
	}
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(2 * time.Second)
	for _, ct := range [][]byte{first, *sent} { // the server kept its copies
		sum := sha256.Sum256(ct)
		e.srv.Inject(x.Address(), hex.EncodeToString(sum[:]), ct)
	}
	if res, err := x.Receive(ctx); !client.NotTheKeys(err) {
		t.Fatalf("a copy taken before the last message was recorded seen: %+v, %v", res, err)
	}
}

// A fault at writing prekeys/generation, once the message is recorded seen,
// stops the drain before the ack, and the Result still says the message: the
// next drain writes the file first, then acks the message as one handled, and
// never says it again. Not parallel: SetStep is the package's.
func TestAFaultAtTheCountsFileStillSaysTheMessage(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "said once")
	file := generationFile(b)
	client.SetStep(func(name string) {
		if name != "delivery seen" {
			return
		}
		// A directory where prekeys/generation is, which no file replaces.
		if err := os.Remove(file); err != nil {
			t.Error(err)
		}
		if err := os.MkdirAll(filepath.Join(file, "x"), 0o700); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() { client.SetStep(nil) })
	res, err := b.Receive(ctx)
	client.SetStep(nil)
	if err == nil || res == nil || len(res.Delivered) != 1 || e.srv.Count(b.Address()) != 1 {
		t.Fatalf("a drain whose count's file cannot be written: %+v, %v", res, err)
	}
	if err := os.RemoveAll(file); err != nil {
		t.Fatal(err)
	}
	res, err = b.Receive(ctx)
	if err != nil || len(res.Delivered) != 0 || e.srv.Count(b.Address()) != 0 {
		t.Fatalf("the drain after: %+v, %v", res, err)
	}
	if generationOf(t, b) != stateGeneration(t, b) {
		t.Fatalf("after the drain: %d in prekeys/generation and %d in the state", generationOf(t, b), stateGeneration(t, b))
	}
}
