package client_test

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// held runs b.Hold for up to wait in a goroutine and returns what it came
// to on the channel.
func held(b *client.Mailbox, wait time.Duration) <-chan *client.HoldResult {
	c := make(chan *client.HoldResult, 1)
	go func() {
		h, err := b.Hold(context.Background(), wait)
		if err != nil {
			h = nil
		}
		c <- h
	}()
	return c
}

func TestAHeldListIsAnsweredWhenAMessageArrives(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b) // an empty mailbox: its tag kept
		c := held(b, time.Hour)
		time.Sleep(time.Minute)
		synctest.Wait()
		select {
		case <-c:
			t.Fatal("the hold ended with nothing sent")
		default:
		}
		send(t, a, b, "while the list was held")
		h := <-c
		if h == nil || !h.Changed || h.After != time.Minute {
			t.Fatalf("the hold came to %+v, want changed after a minute", h)
		}
		if res := receive(t, b); len(res.Delivered) != 1 {
			t.Fatalf("the receive after it delivered %d, want 1", len(res.Delivered))
		}
	})
}

func TestAHeldListWithNothingNewEndsAtTheMailboxsWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		h := <-held(b, time.Hour)
		if h == nil || h.Changed || h.Dropped || h.After != client.DefaultHold {
			t.Fatalf("the hold came to %+v, want unchanged at %v", h, client.DefaultHold)
		}
		// A shorter wait asked is the one held.
		if h := <-held(b, 2*time.Minute); h == nil || h.Changed || h.After != 2*time.Minute {
			t.Fatalf("a 2-minute hold came to %+v", h)
		}
	})
}

// A hold ends before the pass it carries, which the server checks again when
// it answers: a kept pass is held to what it has left, one with no more than
// MinHold left past the margin is replaced, and holds one after another still
// sign a pass every two holds.
func TestAHoldEndsBeforeItsPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		type seen struct {
			pass   string
			answer time.Time // by the clock the mailbox and the server share: when the server answers it with nothing new
			ends   time.Time
		}
		var mu sync.Mutex
		var holds []seen
		var last string
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			a := r.Header.Get("Authorization")
			mu.Lock()
			if r.URL.Query().Has("wait") {
				s, _ := strconv.Atoi(r.URL.Query().Get("wait"))
				p, err := protocol.ParsePass(a)
				if err != nil {
					t.Errorf("a held list's pass: %v", err)
				}
				holds = append(holds, seen{a, e.clock.Now().Add(time.Duration(s) * time.Second), p.NotAfter})
			} else if a != "" {
				last = a
			}
			mu.Unlock()
			next.ServeHTTP(w, r)
		})
		// hold holds the list once, the shared clock moved on as the hold ran.
		hold := func() *client.HoldResult {
			t.Helper()
			h := <-held(b, time.Hour)
			if h == nil || h.Changed {
				t.Fatalf("a hold came to %+v", h)
			}
			e.clock.Add(h.After)
			return h
		}
		receive(t, b) // its tag kept, and a pass signed
		e.clock.Add(protocol.PassLifetime - 2*time.Minute)
		if h := hold(); h.After > 2*time.Minute-30*time.Second || holds[0].pass != last {
			t.Fatalf("a hold on a kept pass with 2 minutes left came to %+v, want that pass, ended before it", h)
		}
		if h := hold(); h.After != client.DefaultHold || holds[1].pass == last {
			t.Fatalf("a hold on a pass with too little left came to %+v, want a new pass and the whole wait", h)
		}
		for range 6 {
			hold()
		}
		passes := map[string]bool{}
		for i, h := range holds {
			if h.answer.After(h.ends) {
				t.Errorf("hold %d is answered at %v, after its pass ends at %v", i, h.answer, h.ends)
			}
			passes[h.pass] = true
		}
		if len(passes) > 1+(len(holds)-1+1)/2 {
			t.Errorf("%d holds carried %d passes, want a pass every two holds", len(holds), len(passes))
		}
	})
}

func TestAHoldLetsTheMailboxsOtherCommandsGoOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		c := held(b, time.Hour)
		time.Sleep(time.Second)
		start := time.Now()
		send(t, b, a, "sent while the mailbox's own list is held")
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("the send waited %v for the hold", waited)
		}
		h := <-c
		if h == nil || h.Changed {
			t.Fatalf("the hold came to %+v, want unchanged: a send changes the recipient's list, not the sender's", h)
		}
	})
}

// A held list answered 410 with the mailbox's own revocation is proved as any
// call's answer is: the mailbox keeps the revocation, and Hold returns it.
func TestAHeldListShownTheMailboxsOwnRevocationKeepsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		rec, err := b.Revocation(ctx)
		if err != nil {
			t.Fatal(err)
		}
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				putRecord(t, next, b.Address(), rec)
				forge.Refuse(w, protocol.CodeMailboxRevoked, func(p *protocol.Problem) { p.Record = rec })
				return
			}
			next.ServeHTTP(w, r)
		})
		if _, err := b.Hold(ctx, time.Hour); !finishedOwn(err) {
			t.Fatalf("the hold: %v, want the mailbox's own revocation", err)
		}
		if s, err := b.Status(ctx); err != nil || !s.Revoked {
			t.Fatalf("the mailbox after the hold: %+v, %v; want its revocation kept", s, err)
		}
	})
}

// path is a network between a mailbox and its server that forgets a
// connection idle for idle: a held list's answer comes through when its
// wait is under idle, or when the mailbox keeps its connection alive below idle
// and the path's keepAlive says probes reach what forgets (a NAT's table,
// not an HTTP proxy's timeout). Otherwise the connection drops at idle: by
// a reset the mailbox sees, or, silent, with the answer never coming.
type path struct {
	idle              time.Duration
	silent, keepAlive bool
}

// on has e's server answer b's held lists through p, and counts them.
func (p path) on(e *env, b *client.Mailbox) *atomic.Int32 {
	var n atomic.Int32
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if !r.URL.Query().Has("wait") {
			next.ServeHTTP(w, r)
			return
		}
		n.Add(1)
		s, _ := strconv.Atoi(r.URL.Query().Get("wait"))
		ka := client.KeepAliveOf(b) // as the connection was dialled: a mailbox's own reads it then
		if time.Duration(s)*time.Second < p.idle || p.keepAlive && ka > 0 && ka < p.idle {
			next.ServeHTTP(w, r)
			return
		}
		if p.silent {
			<-r.Context().Done() // the answer never reaches the mailbox
			return
		}
		time.Sleep(p.idle)
		panic(http.ErrAbortHandler)
	})
	return &n
}

// settle holds b's list until a hold neither drops nor fails, and returns
// how many holds that took, the last one's result and the time it all took.
func settle(t *testing.T, b *client.Mailbox) (int, *client.HoldResult, time.Duration) {
	t.Helper()
	start := time.Now()
	for i := 1; i <= 20; i++ {
		h, err := b.Hold(context.Background(), time.Hour)
		if err == nil && !h.Dropped {
			return i, h, time.Since(start)
		}
	}
	t.Fatalf("not settled in 20 holds, %v", time.Since(start))
	return 0, nil, 0
}

// TestADropIsLearnedInAFewHolds: a mailbox learns how its path forgets a held
// connection within a few holds, with keep-alive below the path's idle
// timeout where probes reach it and its wait kept, and its wait below that
// timeout only where they do not. A drop seen only at the hold's deadline
// halves keep-alive, from the wait, then the wait; one seen as it comes
// puts keep-alive below it.
func TestADropIsLearnedInAFewHolds(t *testing.T) {
	for _, c := range []struct {
		name            string
		p               path
		holds           int
		took            time.Duration
		wait, keepAlive time.Duration
	}{
		// 5m (a wait and its margin, unanswered), then the 4m45s wait.
		{"a NAT at 4 minutes that drops silently", path{4 * time.Minute, true, true}, 2, 9*time.Minute + 45*time.Second,
			client.DefaultHold, 2*time.Minute + 22*time.Second},
		{"a NAT at 2 minutes that drops silently", path{2 * time.Minute, true, true}, 3, 14*time.Minute + 45*time.Second,
			client.DefaultHold, time.Minute + 11*time.Second},
		// The reset comes on the connection the mailbox's first receive left
		// idle, which net/http sends the request again past, on a new
		// connection that keeps alive below it: one hold that drops and is
		// answered, then one that is not dropped.
		{"a NAT at 2 minutes that resets", path{2 * time.Minute, false, true}, 2, 2*time.Minute + 2*client.DefaultHold,
			client.DefaultHold, time.Minute + 45*time.Second},
		// Probes keep nothing alive: twice reset in the first hold, which
		// then shortens the wait below the reset.
		{"a proxy at 2 minutes that resets", path{2 * time.Minute, false, false}, 2, 4*time.Minute + time.Minute + 45*time.Second,
			time.Minute + 45*time.Second, time.Minute + 45*time.Second},
		// Keep-alive halves to its floor (2m22s, 1m11s, 35s, 30s), then the
		// wait halves below the timeout (2m22s, 1m11s).
		{"a proxy at 2 minutes that drops silently", path{2 * time.Minute, true, false}, 7,
			5*(client.DefaultHold+15*time.Second) + 2*time.Minute + 37*time.Second + time.Minute + 11*time.Second,
			time.Minute + 11*time.Second, client.MinHold / 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newMemEnv(t)
				b := e.mailbox()
				receive(t, b)
				c.p.on(e, b)
				holds, h, took := settle(t, b)
				// The last hold neither dropped nor changed: it held its wait.
				if k := client.KeepAliveOf(b); holds != c.holds || took != c.took || h.After != c.wait || k != c.keepAlive {
					t.Fatalf("settled after %d holds in %v, the last holding %v with keep-alive %v; want %d holds in %v, holding %v with keep-alive %v",
						holds, took, h.After, k, c.holds, c.took, c.wait, c.keepAlive)
				}
			})
		})
	}
}

// TestADropAtOnceTeachesNothing: a connection that drops as soon as it
// holds is no network that forgets a silent connection, and changes neither
// keep-alive nor the wait.
func TestADropAtOnceTeachesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				panic(http.ErrAbortHandler)
			}
			next.ServeHTTP(w, r)
		})
		h, err := b.Hold(context.Background(), time.Hour)
		if err == nil || h != nil || client.KeepAliveOf(b) != 0 {
			t.Fatalf("a drop at once came to %+v, %v, keep-alive %v; want an error and nothing learned", h, err, client.KeepAliveOf(b))
		}
	})
}

func TestHoldsStartAtMostOnceAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox() // never received: no tag kept, so each hold ends at once
		start := time.Now()
		var at []time.Duration
		for range 4 {
			h := <-held(b, time.Hour)
			if h == nil || !h.Changed {
				t.Fatalf("a hold with no tag kept came to %+v, want changed", h)
			}
			at = append(at, time.Since(start))
		}
		want := []time.Duration{0, 0, time.Minute, 2 * time.Minute}
		for i := range want {
			if at[i] != want[i] {
				t.Fatalf("holds started at %v, want %v", at, want)
			}
		}
	})
}

// TestAHoldThatFailsAtOnceIsPacedAfterAWake: a held list that fails at once
// is held again at the pace of holdEvery, even after a hold the server
// answered with the list, whose next hold starts at once.
func TestAHoldThatFailsAtOnceIsPacedAfterAWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		c := held(b, time.Hour)
		synctest.Wait()
		send(t, a, b, "wakes the hold")
		if h := <-c; h == nil || !h.Changed {
			t.Fatalf("the hold came to %+v, want changed", h)
		}
		receive(t, b) // the message, which leaves no tag kept
		receive(t, b) // the list empty again, and its tag kept
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const minutes, most = 10, 1 + 2 + 10 // the woken hold's, the burst, one a minute
		var n atomic.Int32
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if !r.URL.Query().Has("wait") {
				next.ServeHTTP(w, r)
				return
			}
			if n.Add(1) > most {
				cancel() // a loop held faster than its pace never lets the clock move
			}
			panic(http.ErrAbortHandler) // every hold dropped at once
		})
		err := b.Await(ctx, time.Now().Add(minutes*time.Minute), nil)
		if err != nil || n.Load() > most || n.Load() < 2 {
			t.Fatalf("Await over %d minutes of holds that fail at once: %v after %d held lists, want nil after 2 to %d", minutes, err, n.Load(), most)
		}
	})
}

// TestAHoldAnsweredAtOnceWithNothingNewIsPaced: a server that answers each
// held list at once, with no message in it, is held at the pace of
// holdEvery: only a drain that found messages starts the next hold at once.
func TestAHoldAnsweredAtOnceWithNothingNewIsPaced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		const minutes, most = 10, 1 + 2 + 10 // the burst, one a minute, and one at the end
		var n atomic.Int32
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				if n.Add(1) > most {
					cancel() // a loop held faster than its pace never lets the clock move
				}
				r.Header.Del("If-None-Match") // answered at once, with the page
			}
			next.ServeHTTP(w, r)
		})
		err := b.Follow(ctx, client.FollowOptions{Until: time.Now().Add(minutes * time.Minute)}, nil)
		if err != nil || n.Load() > most || n.Load() < 2 {
			t.Fatalf("Follow over %d minutes of holds answered at once: %v after %d held lists, want nil after 2 to %d", minutes, err, n.Load(), most)
		}
	})
}

// followNamingAMessageItCannotTake runs b's Follow for ten minutes against
// a server whose list keeps naming one message that b cannot take, as serve
// answers its fetches and acks, and returns how many lists b's drains made.
// More than most cuts it short: a loop that polls faster than its pace
// never lets the clock move.
func followNamingAMessageItCannotTake(t *testing.T, most int32, serve func(w http.ResponseWriter, r *http.Request, next http.Handler) bool) int32 {
	t.Helper()
	var n atomic.Int32
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "listed, and never taken")
		messages := "/v1/" + b.Address().String() + "/messages"
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.Method == http.MethodGet && r.URL.Path == messages && !r.URL.Query().Has("wait") && n.Add(1) > most {
				cancel()
			}
			if strings.HasPrefix(r.URL.Path, messages+"/") && serve(w, r, next) {
				return
			}
			next.ServeHTTP(w, r)
		})
		if err := b.Follow(ctx, client.FollowOptions{Until: time.Now().Add(10 * time.Minute)}, nil); err != nil && ctx.Err() == nil {
			t.Fatal(err)
		}
	})
	return n.Load()
}

// TestAListOfAMessageThatIsGoneIsPaced: a list that keeps naming a message
// whose every fetch is message_not_found takes nothing off it, so it does not
// start the next hold at once: Follow lists at the pace of holdEvery.
func TestAListOfAMessageThatIsGoneIsPaced(t *testing.T) {
	const most = 1 + 2 + 10 // the first drain's, the burst, one a minute
	n := followNamingAMessageItCannotTake(t, most, func(w http.ResponseWriter, r *http.Request, _ http.Handler) bool {
		if r.Method != http.MethodGet {
			return false
		}
		forge.Refuse(w, protocol.CodeMessageNotFound)
		return true
	})
	if n > most || n < 2 {
		t.Fatalf("Follow over 10 minutes of a list naming a message that is gone listed %d times, want 2 to %d", n, most)
	}
}

// TestAListThatStillNamesAnAckedMessageIsPaced: a list that goes on naming a
// message after its ack (lists are eventually consistent, PROTOCOL.md §11.11)
// wakes only the hold after the drain that took it.
func TestAListThatStillNamesAnAckedMessageIsPaced(t *testing.T) {
	const most = 2 + 2 + 10 // the drain that took it and the woken one after, the burst, one a minute
	n := followNamingAMessageItCannotTake(t, most, func(w http.ResponseWriter, r *http.Request, _ http.Handler) bool {
		if r.Method != http.MethodDelete {
			return false
		}
		w.WriteHeader(http.StatusNoContent) // acked, and listed still
		return true
	})
	if n > most || n < 2 {
		t.Fatalf("Follow over 10 minutes of a list that still names an acked message listed %d times, want 2 to %d", n, most)
	}
}

// TestTheDrainAfterAWokenHoldStartsFromItsList: the list a held request is
// answered with is the next drain's first page, so the drain fetches the
// message with no list of its own first: one poll for a hold and its drain.
func TestTheDrainAfterAWokenHoldStartsFromItsList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		c := held(b, time.Hour)
		synctest.Wait()
		sent := send(t, a, b, "wakes the hold")
		if h := <-c; h == nil || !h.Changed {
			t.Fatalf("the hold came to %+v, want changed", h)
		}
		messages := "/v1/" + b.Address().String() + "/messages"
		var mu sync.Mutex
		var asked []string
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if strings.HasPrefix(r.URL.Path, messages) {
				mu.Lock()
				asked = append(asked, r.Method+" "+r.URL.Path)
				mu.Unlock()
			}
			next.ServeHTTP(w, r)
		})
		if res := receive(t, b); len(res.Delivered) != 1 || res.Delivered[0].ID != sent.ID {
			t.Fatalf("the drain after the hold delivered %+v, want the message sent", res.Delivered)
		}
		if want := "GET " + messages + "/" + sent.ID; len(asked) == 0 || asked[0] != want {
			t.Fatalf("the drain after the hold asked %q, want %q first", asked, want)
		}
	})
}

// TestAwaitReturnsForAReceiveEveryHour: with nothing new for longer, Await
// returns at ReceiveEvery, for the Receive that renews the prekey.
func TestAwaitReturnsForAReceiveEveryHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		start := time.Now()
		if err := b.Await(context.Background(), start.Add(3*time.Hour), nil); err != nil || time.Since(start) != client.ReceiveEvery {
			t.Fatalf("Await: %v after %v, want nil at %v", err, time.Since(start), client.ReceiveEvery)
		}
	})
}

// TestAwaitEndsAtARefusalNoWaitMends: a held list refused for good ends
// Await with the refusal, never held again; one the server is to be asked
// again after its Retry-After is held again then.
func TestAwaitEndsAtARefusalNoWaitMends(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		var mu sync.Mutex
		var at []time.Duration
		start := time.Now()
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if !r.URL.Query().Has("wait") {
				next.ServeHTTP(w, r)
				return
			}
			mu.Lock()
			at = append(at, time.Since(start))
			n := len(at)
			mu.Unlock()
			if n == 1 {
				w.Header().Set("Retry-After", "150")
				forge.Refuse(w, protocol.CodeUnavailable)
				return
			}
			forge.Refuse(w, protocol.CodeInvalidRequest)
		})
		var said []error
		err := b.Await(context.Background(), start.Add(time.Hour), func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRetry {
				said = append(said, ev.Err)
			}
			return nil
		})
		var ae *client.APIError
		if !errors.As(err, &ae) || ae.Problem.Code != protocol.CodeInvalidRequest || len(said) != 1 {
			t.Fatalf("Await: %v, having said %v; want the invalid_request refusal, after one retry", err, said)
		}
		if want := []time.Duration{0, 150 * time.Second}; len(at) != 2 || at[0] != want[0] || at[1] != want[1] {
			t.Fatalf("held lists at %v, want %v: the second after the first's Retry-After", at, want)
		}
	})
}

// TestAHoldWhosePassIsRefusedIsChanged: a held list whose pass the server
// refuses is Changed, for the Receive that signs another.
func TestAHoldWhosePassIsRefusedIsChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				forge.Refuse(w, protocol.CodePassExpired)
				return
			}
			next.ServeHTTP(w, r)
		})
		if h, err := b.Hold(context.Background(), time.Hour); err != nil || !h.Changed {
			t.Fatalf("a hold whose pass was refused came to %+v, %v; want changed", h, err)
		}
	})
}

// TestAwaitReturnsByItsDeadline: Await returns by until, however its holds
// fail, its waits for a turn to hold included.
func TestAwaitReturnsByItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				panic(http.ErrAbortHandler) // every hold dropped at once
			}
			next.ServeHTTP(w, r)
		})
		start := time.Now()
		until := start.Add(121 * time.Second)
		if err := b.Await(context.Background(), until, nil); err != nil || time.Since(start) != until.Sub(start) {
			t.Fatalf("Await until %v: %v after %v", until.Sub(start), err, time.Since(start))
		}
	})
}
