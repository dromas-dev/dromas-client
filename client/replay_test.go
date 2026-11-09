package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// A reader whose clock steps back meets a message it has already delivered as
// fresh again. The server keeps what it was sent and lists it once more; the
// reader must refuse it whatever its clock read when it last pruned
// (PROTOCOL.md §12.3 check 8, §15.7).
func TestAReplayIsRefusedAfterTheClockStepsBack(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	ctx := context.Background()
	for _, c := range []struct {
		name        string
		wait        time.Duration // from sent_at to the drain while ahead
		ahead, back time.Duration // the reader's clock then, and at the replay
		serverToo   bool          // the server's clock is moved with the reader's: a hostile server that takes a pass from a clock an honest one refuses, and refuses the renewal it would make
	}{
		{"50m fast at a drain the server takes, then 9m slow", protocol.TTL - 49*time.Minute, 50 * time.Minute, -9 * time.Minute, false},
		{"40 days fast at a recv the server refuses, then right", 5 * 24 * time.Hour, 40 * 24 * time.Hour, 0, false},
		{"62 days fast at a drain a hostile server takes, then right", 5 * 24 * time.Hour, 62 * 24 * time.Hour, 0, true},
		{"control: never ahead", 5 * 24 * time.Hour, 0, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			off := time.Duration(0)
			r, err := client.Init(ctx, t.TempDir(), e.skewed(&off))
			if err != nil {
				t.Fatal(err)
			}
			landAll(t, r)
			s := e.mailbox()
			var kept []byte
			e.srv.SetMiddle(func(w http.ResponseWriter, q *http.Request, next http.Handler) {
				if q.Method == http.MethodPost && strings.HasSuffix(q.URL.Path, "/messages") {
					kept, _ = io.ReadAll(q.Body)
					q.Body = io.NopCloser(bytes.NewReader(kept))
				}
				next.ServeHTTP(w, q)
			})
			send(t, s, r, "pay invoice 7 once")
			if res := receive(t, r); len(res.Delivered) != 1 {
				t.Fatalf("the first drain delivered %d", len(res.Delivered))
			}
			// Renewed just before, at the right clock, so the drain while
			// ahead makes no certificate dated by it: the replay is judged.
			e.clock.Add(c.wait - time.Minute)
			if _, err := r.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
			e.clock.Add(time.Minute)
			off = c.ahead
			if c.serverToo {
				e.clock.Add(c.ahead)
				off = 0
				e.srv.SetMiddle(func(w http.ResponseWriter, q *http.Request, next http.Handler) {
					if q.Method == http.MethodPut && strings.HasSuffix(q.URL.Path, "/prekey") {
						http.Error(w, "down", http.StatusServiceUnavailable)
						return
					}
					next.ServeHTTP(w, q)
				})
			}
			_, err = r.Receive(ctx)
			if c.serverToo {
				e.clock.Add(-c.ahead)
				e.srv.SetMiddle(nil)
			}
			off = c.back
			id := sha256.Sum256(kept)
			e.srv.Inject(r.Address(), hex.EncodeToString(id[:]), kept)
			res, err2 := r.Receive(ctx)
			t.Logf("the drain while ahead: %v; the replay: delivered %d, refused %v, %v", client.Code(err), len(res.Delivered), res.Refused, err2)
			if len(res.Delivered) != 0 || err2 != nil {
				t.Error("a message already delivered was delivered again, or the replay was not judged")
			}
			// Past what the replay set reaches back over, the prekey it was
			// sealed to is gone too, and it is refused as undecryptable.
			if c.serverToo && len(res.Refused) != 1 {
				t.Errorf("the replay after a step back of 62 days: refused %v, want one refusal", res.Refused)
			}
		})
	}
}

// A recv refused for its clock prunes nothing, whether its server refuses the
// pass or the mailbox its own certificate first: the seen-set is pruned only once
// the server has taken a pass dated by that clock.
func TestARefusedDrainPrunesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	off := time.Duration(0)
	r, err := client.Init(ctx, t.TempDir(), e.skewed(&off))
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, r)
	send(t, e.mailbox(), r, "hello")
	receive(t, r)
	before, ok := stateOf(r, "seen")
	if !ok || len(before) == 0 {
		t.Fatal("no replay set after a delivery")
	}
	// Far enough ahead that a prune by this clock would drop the line.
	off = protocol.SeenFor + 4*24*time.Hour
	if _, err := r.Receive(ctx); err == nil {
		t.Fatalf("a drain %v ahead was taken", off)
	}
	if after, _ := stateOf(r, "seen"); !bytes.Equal(before, after) {
		t.Errorf("a refused drain changed the replay set: %q, then %q", before, after)
	}
	if has(r, "seen-floor") {
		t.Error("a refused drain raised the floor")
	}
}

// A state file lost with its prekeys kept is a failure, and its owner is told
// once: the state names none of the keys, so recv stops before it lists
// anything, and status says so, until dromas init --restore adopts them and
// renews. The failure's cost is accepted: the copy the server kept of a message
// the lost state delivered is delivered again, and the prune's floor is not
// raised. A message to the new prekey is delivered (PROTOCOL.md §15.9).
func TestALostStateStopsUntilAcknowledgedAndAcceptsItsCost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "delivered before the state file was lost")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the first delivery: %+v", res)
	}
	x := lostUnacknowledged(t, e, b)
	e.clock.Add(2 * time.Second)
	id := sha256.Sum256(*sent)
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // the server kept its copy
	if _, err := x.Receive(ctx); !client.NotTheKeys(err) || e.srv.Count(x.Address()) != 1 {
		t.Fatalf("recv before the restore is acknowledged: %v", err)
	}
	if st, err := x.Status(ctx); err != nil || !slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, "dromas init --restore") }) {
		t.Fatalf("status before the restore is acknowledged: %q, %v", st.Attention, err)
	}
	restoreAcknowledged(t, e, x, 1)
	if n := numberOf(t, x); slices.Contains(client.AdoptedKeys(t, x), n) {
		t.Fatalf("init --restore published no prekey of its own: %d is the key it adopted", n)
	}
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitedDelivered(t, res, a, 1)
	if has(x, "seen-floor") {
		t.Fatal("the restore raised the prune's floor, which holds every message")
	}
	send(t, a, x, "to the new prekey")
	if res := receive(t, x); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message to the restore's new prekey: %+v, want it delivered", res)
	}
}

// A state file restored from a copy older than a renewal, the prekeys left as
// they are, does not name the renewal's key, so recv stops before it lists
// anything until dromas init --restore adopts that key: a message the lost
// state delivered under that key is then delivered again (PROTOCOL.md §15.9).
func TestAStateRestoredFromAnOlderCopyAdoptsTheNewerKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	path := filepath.Join(b.Dir(), "state")
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "sealed to the renewal's key")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the first delivery: %+v", res)
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
	restoreAcknowledged(t, e, x, 1)
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitedDelivered(t, res, a, 1)
}
