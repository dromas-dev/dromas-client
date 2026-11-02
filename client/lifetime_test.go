package client_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// A mailbox's prekeys are valid for protocol.PrekeyLifetime, or for the shorter
// lifetime its owner sets, down to client.MinPrekeyLifetime (PROTOCOL.md
// §12.4). Each client
// records when it signed a prekey, and the mailbox times its renewals and the
// early deletion of its retired prekeys by that record, never by the
// statement's not_after, which times a retired prekey's latest deletion.

// lifetimeOf sets b's prekey lifetime.
func lifetimeOf(t *testing.T, b *client.Mailbox, d time.Duration) {
	t.Helper()
	if err := b.SetPrekeyLifetime(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

// madeOf is what b's record says of when prekey n was signed.
func madeOf(t *testing.T, b *client.Mailbox, n uint64) string {
	t.Helper()
	return string(statement(t, b, n, ".made"))
}

// A prekey is made for the mailbox's lifetime, and its record says when: the
// default, the shortest and one between.
func TestAPrekeyIsMadeForTheMailboxsLifetime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	st, err := b.Status(t.Context())
	if err != nil || st.PrekeyLifetime != int64(protocol.PrekeyLifetime/time.Second) || st.PrekeyMade != e.clock.Now().Format(protocol.TimeLayout) {
		t.Fatalf("a new mailbox's status: %+v, %v", st, err)
	}
	for _, d := range []time.Duration{client.MinPrekeyLifetime, 3 * 24 * time.Hour, protocol.PrekeyLifetime} {
		lifetimeOf(t, b, d)
		e.clock.Add(time.Second)
		p, err := b.Rekey(ctx)
		if err != nil {
			t.Fatal(err)
		}
		now := e.clock.Now()
		if !p.NotAfter.Equal(now.Add(d)) {
			t.Errorf("a lifetime of %v: the prekey ends at %v, %v after it was made", d, p.NotAfter, p.NotAfter.Sub(now))
		}
		if got := madeOf(t, b, p.Serial); got != now.Format(protocol.TimeLayout) {
			t.Errorf("a lifetime of %v: the record says %q", d, got)
		}
		st, err := b.Status(t.Context())
		if err != nil || st.PrekeyLifetime != int64(d/time.Second) || st.PrekeyMade != now.Format(protocol.TimeLayout) || strings.Contains(strings.Join(st.Attention, " "), "lifetime") {
			t.Errorf("a lifetime of %v: status %+v, %v", d, st, err)
		}
	}
}

// Receive renews a prekey once it is RenewAt old, and not before: a day at
// the default lifetime, 22 hours at 25 hours, 5 hours and 45 minutes at the
// shortest. Then RecvWithin is what is left of it, less the room for the
// renewal's calls and senders' clocks.
func TestRenewAtAndRecvWithin(t *testing.T) {
	t.Parallel()
	h := time.Hour
	for _, c := range []struct{ life, at, within time.Duration }{
		{protocol.PrekeyLifetime, 24 * h, 6*24*h + 22*h},
		{25 * h, 22 * h, h},
		{24 * h, 21 * h, h},
		{client.MinPrekeyLifetime, 5*h + 45*time.Minute, 5 * time.Minute},
	} {
		if at, within := client.RenewAt(c.life), client.RecvWithin(c.life); at != c.at || within != c.within {
			t.Errorf("a lifetime of %v: renewed at %v, received within %v; want %v and %v", c.life, at, within, c.at, c.within)
		}
	}
	if client.MinPrekeyLifetime != 6*h {
		t.Errorf("the shortest lifetime is %v", client.MinPrekeyLifetime)
	}
}

// Receive renews a short-lived prekey once it is RenewAt old, and not before:
// its age is its record's, where not_after less protocol.PrekeyLifetime would
// make it old at once, and renew it at every receive. Senders reach the mailbox
// throughout.
func TestAShortLivedPrekeyIsRenewedAtRenewAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, life := range []time.Duration{client.MinPrekeyLifetime, 25 * time.Hour} {
		t.Run(life.String(), func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			lifetimeOf(t, b, life)
			if _, err := b.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
			at := client.RenewAt(life)
			for age := at / 4; age <= at; age += at / 4 {
				e.clock.Add(at / 4)
				if res := receive(t, b); res.Renewed != nil {
					t.Fatalf("renewed a prekey %v old", age)
				}
			}
			e.clock.Add(time.Second)
			send(t, a, b, "with what RenewAt leaves of the prekey")
			res := receive(t, b)
			if res.Renewed == nil || len(res.Delivered) != 1 {
				t.Fatalf("no renewal of a prekey RenewAt old: %+v", res)
			}
			if !res.Renewed.NotAfter.Equal(e.clock.Now().Add(life)) {
				t.Fatalf("the renewal ends at %v", res.Renewed.NotAfter)
			}
			send(t, a, b, "to the renewed prekey")
			if res := receive(t, b); res.Renewed != nil || len(res.Delivered) != 1 {
				t.Fatalf("after the renewal: %+v", res)
			}
		})
	}
}

// A lifetime set lower holds from the next receive, which renews a prekey
// made for longer; one set higher waits for the day-old renewal.
func TestALowerLifetimeHoldsFromTheNextReceive(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	lifetimeOf(t, b, 2*24*time.Hour)
	res := receive(t, b)
	if res.Renewed == nil || !res.Renewed.NotAfter.Equal(e.clock.Now().Add(2*24*time.Hour)) {
		t.Fatalf("a lower lifetime, at the next receive: %+v", res.Renewed)
	}
	if res := receive(t, b); res.Renewed != nil {
		t.Fatal("renewed again at the receive after")
	}
	lifetimeOf(t, b, protocol.PrekeyLifetime)
	if res := receive(t, b); res.Renewed != nil {
		t.Fatal("a higher lifetime renewed at once")
	}
}

// A prekey with no record of when it was made is renewed at the next
// receive, and while the mailbox knows neither when the newest retired prekey
// was made nor when its server took it or the current one, every retired key
// is kept until its deletion time.
func TestAPrekeyWithNoRecordIsRenewedAndItsKeysKept(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	k1 := numberOf(t, b)
	e.clock.Add(25 * time.Hour)
	receive(t, b)
	k2 := numberOf(t, b)
	b.SetRecord(t, "prekeys/"+strconv.FormatUint(k2, 10)+".made", nil)
	b.SetRecord(t, "prekeys/"+strconv.FormatUint(k2, 10)+".taken", nil)
	st, err := b.Status(t.Context())
	if err != nil || st.PrekeyMade != "" {
		t.Fatalf("status with no record: %+v, %v", st, err)
	}
	e.clock.Add(time.Second)
	res := receive(t, b)
	if res.Renewed == nil {
		t.Fatal("a prekey with no record was not renewed")
	}
	b.SetRecord(t, "prekeys/"+strconv.FormatUint(res.Renewed.Serial, 10)+".taken", nil)
	e.clock.Add(2 * time.Hour)
	receive(t, b)
	if !exists(prekeyFile(b, k1, ".agekey")) || !exists(prekeyFile(b, k2, ".agekey")) {
		t.Fatal("a retired key was deleted while the newest retired one has no record")
	}
}

// A lifetime that is out of range, or not a whole number of seconds, is refused
// before anything of the mailbox is read, and one in range is what it says.
func TestALifetimeOutOfRangeIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	dir := e.mailbox().Dir()
	for _, d := range []time.Duration{client.MinPrekeyLifetime - time.Second, protocol.PrekeyLifetime + time.Second, client.MinPrekeyLifetime + time.Millisecond} {
		opt := e.opts()
		opt.PrekeyLifetime = d
		if _, err := client.Open(dir, opt); err == nil || !strings.Contains(err.Error(), "no prekey lifetime") {
			t.Errorf("a lifetime of %v: %v", d, err)
		}
	}
	opt := e.opts()
	opt.PrekeyLifetime = client.MinPrekeyLifetime
	b, err := client.Open(dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b.PrekeyLifetime(t.Context()); err != nil || got != client.MinPrekeyLifetime {
		t.Errorf("%v, %v", got, err)
	}
}

// At the shortest lifetime that leaves an hour for clocks, a day, the two
// clocks furthest apart that a server takes, a mailbox 9 minutes slow and a
// sender 49 fast (PROTOCOL.md §15.7), still meet: the mailbox renews once its
// prekey is RenewAt old, which leaves that sender two hours of it. At the
// shortest lifetime, which leaves five minutes, clocks that far apart meet.
func TestTheShortestLifetimesAtTheClocksFurthestApart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct{ life, slow, fast time.Duration }{
		{24 * time.Hour, -9 * time.Minute, 49 * time.Minute},
		{client.MinPrekeyLifetime, -2 * time.Minute, 2*time.Minute + 59*time.Second},
	} {
		t.Run(c.life.String(), func(t *testing.T) {
			e := newEnv(t)
			slow, fast := c.slow, c.fast
			b, err := client.Init(ctx, t.TempDir(), e.skewed(&slow))
			if err != nil {
				t.Fatal(err)
			}
			landAll(t, b)
			a, err := client.Init(ctx, t.TempDir(), e.skewed(&fast))
			if err != nil {
				t.Fatal(err)
			}
			lifetimeOf(t, b, c.life)
			if _, err := b.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
			e.clock.Add(client.RenewAt(c.life) + time.Second)
			send(t, a, b, "with what RenewAt leaves of the prekey, by the sender's clock")
			if res := receive(t, b); res.Renewed == nil || len(res.Delivered) != 1 {
				t.Fatalf("the slow mailbox's receive: %+v", res)
			}
			e.clock.Add(c.life - client.RenewAt(c.life))
			send(t, a, b, "to the renewed prekey, after the first ran out")
			if res := receive(t, b); len(res.Delivered) != 1 {
				t.Fatalf("after the first prekey ran out: %+v", res)
			}
		})
	}
}

// The documented cadence, a recv every RecvWithin of the lifetime, renews with
// the room for the renewal's calls and for senders' clocks left of the prekey
// by the mailbox's clock, so a sender fast of it by up to that room for
// clocks, which a server takes (§15.7), is never refused: at the shortest
// lifetime, at the shortest that leaves an hour for clocks, at one a little
// longer and at the default, with recv a minute short of RenewAt the first
// time. Sixty sends span the room left at the second recv.
func TestTheDocumentedCadenceLeavesRoomForAClock(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	ctx := context.Background()
	for _, c := range []struct {
		life          time.Duration
		mailbox, from time.Duration // the mailbox's clock and the sender's, from the server's
	}{
		{client.MinPrekeyLifetime, 0, 2 * time.Minute},
		{client.MinPrekeyLifetime, -2 * time.Minute, 2*time.Minute + 59*time.Second},
		{24 * time.Hour, -9 * time.Minute, 49 * time.Minute},
		{3*24*time.Hour + time.Hour, 0, 30 * time.Minute},
		{protocol.PrekeyLifetime, -9 * time.Minute, 49 * time.Minute},
	} {
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
		if _, err := b.Rekey(ctx); err != nil {
			t.Fatal(err)
		}
		every := client.RecvWithin(c.life)
		left := c.life - client.RenewAt(c.life) - every   // what is left of the prekey at the second recv
		e.clock.Add(client.RenewAt(c.life) - time.Minute) // a recv a minute short of RenewAt
		if res := receive(t, b); res.Renewed != nil {
			t.Fatal("renewed early")
		}
		step := left / 60
		e.clock.Add(every - left/2)
		refused := 0
		for i := 1; i <= 60; i++ {
			e.clock.Add(step)
			if i == 30 {
				receive(t, b) // the next recv, every after the last
			}
			if _, err := a.Send(ctx, b.Address(), "a message"); err != nil {
				refused++
			}
		}
		if refused > 0 {
			t.Errorf("lifetime %v, recv every %v, mailbox %v and sender %v off: refused %d of 60 sends, %v apart", c.life, every, c.mailbox, c.from, refused, step)
		}
	}
}

// A renewal the server never took waits, signed for protocol.PrekeyLifetime, and the owner
// then sets the shortest lifetime. PROTOCOL.md §12.4: "A lifetime set lower
// holds from the next recv, which renews a prekey made for longer." The one
// that waited is spent, and a new key is made for the lifetime now set.
func TestALowerLifetimeHoldsOverAWaitingRenewal(t *testing.T) {
	t.Parallel()
	for _, via := range []string{"recv", "prekey renew"} {
		t.Run(via, func(t *testing.T) {
			e := newEnv(t)
			b := e.mailbox()
			e.clock.Add(25 * time.Hour)
			forge.FailOnce(e.srv, http.MethodPut, "/prekey") // the server is down: it never sees the upload
			if res := receive(t, b); res.RenewErr == nil {
				t.Fatal("the renewal did not fail")
			}
			lifetimeOf(t, b, client.MinPrekeyLifetime)
			e.clock.Add(time.Hour)
			var p *protocol.Prekey
			if via == "recv" {
				p = receive(t, b).Renewed
			} else {
				var err error
				if p, err = b.Rekey(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if p == nil {
				t.Fatal("no renewal")
			}
			made, err := protocol.ParseTime(strings.TrimSuffix(madeOf(t, b, p.Serial), "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if life := p.NotAfter.Sub(made); life > client.MinPrekeyLifetime {
				t.Errorf("%s published a prekey made for %v after the lifetime was set to %v", via, life, client.MinPrekeyLifetime)
			}
		})
	}
}
