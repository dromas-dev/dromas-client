package client_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// rightAtFirstCall sets a mailbox's clock right (off = 0) as its first request
// goes out: a clock that read ahead when the command began and was stepped
// back before anything was signed or sent.
type rightAtFirstCall struct{ off *time.Duration }

func (j rightAtFirstCall) RoundTrip(r *http.Request) (*http.Response, error) {
	*j.off = 0
	return http.DefaultTransport.RoundTrip(r)
}

// A renewal whose mailbox's clock reads 70 days ahead when the command begins and
// is right before its first request: every pass the server takes is dated by
// the right clock, so no key a message waits for is deleted, and the message sealed to
// the retired prekey is delivered.
func TestAClockSteppedBackBeforeARenewalDeletesNoKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "first, sealed to the prekey about to retire")
	send(t, a, b, "second, sealed to it too")
	e.clock.Add(2 * time.Second)
	keys, _ := filepath.Glob(filepath.Join(b.Dir(), "keys", "prekeys", "*.agekey"))
	off := 70 * 24 * time.Hour
	o := e.skewed(&off)
	o.HTTPClient = &http.Client{Transport: rightAtFirstCall{&off}}
	stepped, err := client.Open(b.Dir(), o)
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := stepped.Rekey(ctx)
	after, _ := filepath.Glob(filepath.Join(b.Dir(), "keys", "prekeys", "*.agekey"))
	t.Logf("renewal: %v; prekey keys %d before, %d after (the new one among them)", rerr, len(keys), len(after))
	right, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	res, err := right.Receive(ctx)
	if res == nil {
		res = &client.Result{}
	}
	t.Logf("then: delivered %d, refused %d, %v", len(res.Delivered), len(res.Refused), err)
	for _, r := range res.Refused {
		t.Logf("  refused: %s %s", r.Code, r.Reason)
	}
	if rerr != nil || len(after) < len(keys)+1 || len(res.Delivered) != 2 {
		t.Errorf("a clock stepped back before the renewal's first request deleted the key the waiting message needs")
	}
}

// A drain that empties the mailbox deletes the older retired prekeys by its
// clock only as it read when the server answered the drain's list, never by
// a later reading (PROTOCOL.md §12.4). Here Receive's
// first reading is 2 hours ahead and the clock is right again before the
// list: the key that the list's reading keeps must be kept.
func TestDropRetiredDeletesByNoReadingLaterThanTheLists(t *testing.T) {
	t.Parallel()
	for _, ahead := range []time.Duration{0, 2 * time.Hour} {
		ctx := context.Background()
		e := newEnv(t)
		var first atomic.Bool
		o := e.opts()
		o.Now = func() time.Time {
			if first.CompareAndSwap(true, false) {
				return e.clock.Now().Add(ahead)
			}
			return e.clock.Now()
		}
		b, err := client.Init(ctx, t.TempDir(), o)
		if err != nil {
			t.Fatal(err)
		}
		k1 := numberOf(t, b)
		for range 2 {
			e.clock.Add(2 * time.Second)
			if _, err := b.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
		}
		e.clock.Add(5 * time.Minute)
		first.Store(true) // the next reading is Receive's first
		if _, err := b.Receive(ctx); err != nil {
			t.Fatal(err)
		}
		kept := exists(prekeyFile(b, k1, ".agekey"))
		t.Logf("Receive's first reading %v ahead, then right: the oldest retired key, retired 5 minutes before, kept: %v", ahead, kept)
		if !kept {
			t.Errorf("Receive's first reading %v ahead deleted a key retired 5 minutes before, which the list's reading keeps", ahead)
		}
	}
}

// A drain mended part way deletes by the first list of the drain after the renewal,
// which is the drain that emptied the mailbox, and never by the date of the drain
// the server refused. That list is three hours after the server took the newest
// retired prekey, so the prekey older than it goes. It is under an hour after the
// server took the renewal, so the newest retired prekey stays.
func TestADrainMendedPartWayDeletesByTheListOfTheDrainAfterIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	off := -9 * time.Minute // the mailbox's clock, apart from the server's
	b, err := client.Init(ctx, t.TempDir(), e.skewed(&off))
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, b)
	a := e.mailbox()
	oldest := numberOf(t, b)
	e.clock.Add(2 * time.Second)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	newest := numberOf(t, b)
	send(t, a, b, "one")
	send(t, a, b, "two")
	e.srv.SetPage(1)
	list := "/v1/" + b.Address().String() + "/messages"
	var refused atomic.Bool
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodGet && r.URL.Path == list && r.URL.Query().Get("cursor") != "" && refused.CompareAndSwap(false, true) {
			e.clock.Add(3 * time.Hour)
			forge.Refuse(w, protocol.CodeMailboxNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
	res := receive(t, b)
	if !refused.Load() || res.Renewed == nil || len(res.Delivered) != 2 {
		t.Fatalf("the drain mended part way: %+v", res)
	}
	if exists(prekeyFile(b, oldest, ".agekey")) {
		t.Error("the date of the refused drain kept a prekey the drain after the renewal deletes")
	}
	if !exists(prekeyFile(b, newest, ".agekey")) {
		t.Error("the newest retired prekey went within an hour of the renewal that retired it")
	}
}

// RecvWithin's cadence leaves the prekey the room for senders' clocks and for
// the renewal's calls by the mailbox's clock when recv begins, and recv's
// renewal reads the mailbox's own mailbox before it publishes. At a lifetime of
// a day, the shortest that leaves an hour for clocks, a mailbox 9m59s slow and
// a sender 50m fast, the two clocks furthest apart a server takes; at the
// shortest lifetime, which leaves five minutes, two clocks 4m59s apart. recv
// runs exactly RenewAt after the prekey was made (renewDue renews once it is
// older), then exactly RecvWithin later. The sender sends every second from 3
// seconds before the second recv to 3 seconds after it, and once while the
// renewal's read is answered.
func TestARenewalAtRecvWithinWhoseReadTakesSeconds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	day := 24 * time.Hour
	slow, fast := -(10*time.Minute - time.Second), 50*time.Minute
	for _, c := range []struct {
		name          string
		life          time.Duration
		mailbox, from time.Duration
		delay         time.Duration // how long the renewal's read of the mailbox's own mailbox takes
	}{
		{"a day, the read takes 5s", day, slow, fast, 5 * time.Second},
		{"control: a day, the read takes no time", day, slow, fast, 0},
		{"control: a day, the read takes 5s, the mailbox right and the sender 30m fast", day, 0, 30 * time.Minute, 5 * time.Second},
		{"the shortest lifetime, the read takes 5s", client.MinPrekeyLifetime, -2 * time.Minute, 2*time.Minute + 59*time.Second, 5 * time.Second},
		{"control: the shortest lifetime, the read takes 5s, the mailbox right and the sender 2m fast", client.MinPrekeyLifetime, 0, 2 * time.Minute, 5 * time.Second},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			mailboxOff, fromOff := c.mailbox, c.from
			b, err := client.Init(ctx, t.TempDir(), e.skewed(&mailboxOff))
			if err != nil {
				t.Fatal(err)
			}
			landAll(t, b)
			a, err := client.Init(ctx, t.TempDir(), e.skewed(&fromOff))
			if err != nil {
				t.Fatal(err)
			}
			lifetimeOf(t, b, c.life)
			p, err := b.Rekey(ctx)
			if err != nil {
				t.Fatal(err)
			}
			made, _ := protocol.ParseTime(strings.TrimSpace(madeOf(t, b, p.Serial)))
			e.clock.Add(made.Add(client.RenewAt(c.life)).Sub(e.clock.Now().Add(mailboxOff))) // the mailbox's clock reads made + RenewAt
			if res := receive(t, b); res.Renewed != nil {
				t.Fatal("renewed at exactly RenewAt old")
			}
			var refused, sends atomic.Int32
			sendOne := func() {
				sends.Add(1)
				if _, err := a.Send(ctx, b.Address(), "a message"); err != nil {
					refused.Add(1)
				}
			}
			own := "/v1/" + b.Address().String()
			var armed atomic.Bool
			e.srv.SetMiddle(func(w http.ResponseWriter, q *http.Request, next http.Handler) {
				if c.delay > 0 && q.Method == http.MethodGet && q.URL.Path == own && armed.CompareAndSwap(true, false) {
					e.clock.Add(c.delay)
					sendOne() // a send while the renewal is under way
				}
				next.ServeHTTP(w, q)
			})
			e.clock.Add(client.RecvWithin(c.life) - 3*time.Second)
			if _, err := a.Receive(ctx); err != nil { // the sender renews, so its certificate signs messages
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				sendOne()
				e.clock.Add(time.Second)
			}
			armed.Store(true)
			res := receive(t, b) // at exactly made + RenewAt + RecvWithin by the mailbox's clock
			for i := 0; i < 3; i++ {
				e.clock.Add(time.Second)
				sendOne()
			}
			t.Logf("mailbox %v, sender %v, the read %v: renewed %v, %d of %d sends refused", c.mailbox, c.from, c.delay, res.Renewed != nil, refused.Load(), sends.Load())
			if refused.Load() > 0 {
				t.Error("a sender whose clock a server takes was refused at RecvWithin's cadence")
			}
		})
	}
}
