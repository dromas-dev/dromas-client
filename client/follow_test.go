package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// follow runs b's loop until its context ends, handing each event to events.
func follow(ctx context.Context, b *client.Mailbox, o client.FollowOptions, events chan<- client.Event) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- b.Follow(ctx, o, func(_ context.Context, ev client.Event) error {
			events <- ev
			return nil
		})
	}()
	return done
}

// nextDelivery is the next delivery the loop hands over.
func nextDelivery(t *testing.T, events <-chan client.Event) *client.Delivery {
	t.Helper()
	for ev := range events {
		if ev.Kind == client.EventDelivery {
			return ev.Delivery
		}
	}
	t.Fatal("the loop ended")
	return nil
}

// oneDrain is a loop that runs one drain.
func oneDrain(owed bool) client.FollowOptions {
	return client.FollowOptions{Until: time.Now(), Owed: owed}
}

// The loop goes on after each delivery, each handed over as it arrives with
// no time passed, and ends when its context does, with that context's error.
func TestFollowGoesOnAfterEachDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan client.Event, 64)
		done := follow(ctx, b, client.FollowOptions{}, events)
		for _, body := range []string{"the first", "the second", "the third"} {
			time.Sleep(time.Minute)
			synctest.Wait()
			sent := time.Now()
			send(t, a, b, body)
			if d := nextDelivery(t, events); bodyOf(t, *d) != body || time.Since(sent) != 0 {
				t.Fatalf("%q handed over after %s, want %q at once", bodyOf(t, *d), time.Since(sent), body)
			}
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Follow, its context ended: %v", err)
		}
	})
}

// The loop ends at Until, by a last drain that takes what arrived.
func TestFollowEndsAtItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		start := time.Now()
		var drains int
		err := b.Follow(context.Background(), client.FollowOptions{Until: start.Add(10 * time.Minute)}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventDrained {
				drains++
			}
			return nil
		})
		if err != nil || time.Since(start) != 10*time.Minute || drains != 2 {
			t.Fatalf("Follow: %v after %s, in %d drains; want nil at its deadline, in 2", err, time.Since(start), drains)
		}
	})
}

// Each thing a drain does is one event, in the order its kinds are declared,
// and the mailbox's lock is free at each: a handler runs any command of the
// mailbox.
func TestFollowHandsOverEachKindWithTheLockFree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	allowed, stranger, blocked, b := e.mailbox(), e.mailbox(), e.mailbox(), e.heldMailbox()
	if _, err := b.Allow(ctx, allowed.Address()); err != nil {
		t.Fatal(err)
	}
	send(t, allowed, b, "delivered")
	send(t, stranger, b, "held")
	send(t, blocked, b, "blocked")
	if _, err := b.Block(ctx, blocked.Address()); err != nil {
		t.Fatal(err)
	}
	junk := []byte("no age file")
	id := sha256.Sum256(junk)
	e.srv.Inject(b.Address(), hex.EncodeToString(id[:]), junk)
	var kinds []client.EventKind
	var seen []string
	err := b.Follow(ctx, oneDrain(false), func(ctx context.Context, ev client.Event) error {
		kinds = append(kinds, ev.Kind)
		short, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := b.SetAlias(short, "lock-is-free", allowed.Address()); err != nil {
			t.Errorf("the lock at event %d: %v", ev.Kind, err)
		}
		switch ev.Kind {
		case client.EventDelivery, client.EventHeld:
			seen = append(seen, bodyOf(t, *ev.Delivery))
		case client.EventRefusal:
			seen = append(seen, ev.Refusal.Code)
		case client.EventBlocked:
			if ev.ID == "" {
				t.Error("a blocked message with no id")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []client.EventKind{client.EventDelivery, client.EventRefusal, client.EventHeld, client.EventBlocked, client.EventDrained}
	if !slices.Equal(kinds, want) {
		t.Fatalf("kinds %v, want %v", kinds, want)
	}
	if !slices.Equal(seen, []string{"delivered", client.RefusedDecryptError, "held"}) {
		t.Fatalf("messages %v", seen)
	}
}

// A handler's error ends the loop, as that error or, for ErrStop, as nil, after
// the rest of the drain's events: the drain has acked those messages, so
// a handler would otherwise never see them.
func TestAHandlersErrorEndsTheLoopAfterItsDrainsEvents(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	for _, c := range []struct {
		name          string
		give, return_ error
	}{{"an error", boom, boom}, {"ErrStop", client.ErrStop, nil}} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "one")
			send(t, a, b, "two")
			var got []string
			err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
				if ev.Kind != client.EventDelivery {
					return nil
				}
				got = append(got, bodyOf(t, *ev.Delivery))
				return c.give
			})
			if !errors.Is(err, c.return_) || (c.return_ == nil && err != nil) || len(got) != 2 {
				t.Fatalf("Follow: %v, having handed over %q", err, got)
			}
		})
	}
}

// The loop ends at a refusal no wait mends, as Fatal says, and a drain that a
// wait may mend is an EventRetry, drained again after its Retry-After.
func TestFollowRetriesWhatAWaitMendsAndEndsAtWhatNoWaitDoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		var mu sync.Mutex
		var at []time.Duration
		start := time.Now()
		mine := "/v1/" + b.Address().String() + "/messages"
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.Method == http.MethodGet && r.URL.Path == mine {
				mu.Lock()
				at = append(at, time.Since(start))
				n := len(at)
				mu.Unlock()
				switch n {
				case 1:
					w.Header().Set("Retry-After", "150")
					forge.Refuse(w, protocol.CodeUnavailable)
				default:
					forge.Refuse(w, protocol.CodeInvalidRequest)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
		var retried []error
		err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRetry {
				retried = append(retried, ev.Err)
			}
			return nil
		})
		if !client.Fatal(err) || client.Code(err) != protocol.CodeInvalidRequest || len(retried) != 1 || client.Fatal(retried[0]) {
			t.Fatalf("Follow: %v, having retried %v", err, retried)
		}
		if want := []time.Duration{0, 150 * time.Second}; !slices.Equal(at, want) {
			t.Fatalf("lists at %v, want %v", at, want)
		}
	})
}

// Past the deadline a retry cannot come, so the drain's error ends the loop,
// and it is one to retry: no Fatal.
func TestADrainThatFailsAtTheDeadlineEndsTheLoop(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	mine := "/v1/" + b.Address().String() + "/messages"
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodGet && r.URL.Path == mine {
			forge.Refuse(w, protocol.CodeUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
	err := b.Follow(context.Background(), oneDrain(false), nil)
	if err == nil || client.Fatal(err) || !client.Transient(err) {
		t.Fatalf("Follow: %v", err)
	}
}

// Fatal and Transient tell each failure a run ends on: one that starting
// again gains nothing from, one that it may, and one that a wait may mend.
func TestFatalTellsARefusalNoWaitMendsFromARetry(t *testing.T) {
	t.Parallel()
	apiError := func(status int, c protocol.Code) error {
		return &client.APIError{Status: status, Problem: protocol.Problem{Code: c}}
	}
	// A pass_expired is a clock behind ([client.NotReady]) only where the
	// server dates its answer later than this machine's clock reads.
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	passExpired := func(dated, read time.Time) error {
		return &client.APIError{Status: http.StatusUnauthorized, Problem: protocol.Problem{Code: protocol.CodePassExpired}, Dated: dated, Read: read}
	}
	for _, c := range []struct {
		name             string
		err              error
		fatal, transient bool
	}{
		{"no error", nil, false, false},
		{"a revocation", &client.RevokedError{}, true, false},
		{"another format", &client.FormatError{}, true, false},
		{"a client's refusal", &client.RefusedError{Reason: "no"}, true, false},
		{"no mailbox", apiError(http.StatusNotFound, protocol.CodeMailboxNotFound), true, false},
		{"bad request", apiError(http.StatusBadRequest, protocol.CodeInvalidRequest), true, false},
		{"a server error", apiError(http.StatusServiceUnavailable, protocol.CodeUnavailable), false, true},
		{"a rate limit", apiError(http.StatusTooManyRequests, protocol.CodeRateLimited), false, true},
		{"the lock", client.ErrLocked, false, true},
		{"a fault of the disk", errors.New("no space left on device"), false, false},
		{"pass_expired, dated later than the clock", passExpired(now, now.Add(-time.Second)), false, true},
		{"pass_expired, dated the clock's second", passExpired(now, now), true, false},
		{"pass_expired, dated earlier than the clock", passExpired(now, now.Add(time.Hour)), true, false},
		{"pass_expired, with no Date", passExpired(time.Time{}, now), true, false},
		{"pass_expired, with no reading of the clock", passExpired(now, time.Time{}), true, false},
	} {
		if got := client.Fatal(c.err); got != c.fatal {
			t.Errorf("Fatal(%s) = %v", c.name, got)
		}
		if got := client.Transient(c.err); got != c.transient {
			t.Errorf("Transient(%s) = %v", c.name, got)
		}
	}
}

// A handler's error ends Await, as that error.
func TestAHandlersErrorEndsAwait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				forge.Refuse(w, protocol.CodeUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
		boom := errors.New("boom")
		err := b.Await(context.Background(), time.Now().Add(time.Hour), func(context.Context, client.Event) error { return boom })
		if !errors.Is(err, boom) {
			t.Fatalf("Await: %v", err)
		}
	})
}

// A mailbox that keeps its own revocation ends the loop at once, as Fatal.
func TestFollowEndsAtTheMailboxsOwnRevocation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	rec, err := b.Revocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.KeepRevocation(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	err = b.Follow(context.Background(), client.FollowOptions{}, nil)
	var re *client.RevokedError
	if !errors.As(err, &re) || !client.Fatal(err) {
		t.Fatalf("Follow: %v", err)
	}
}

// A running loop renews as Receive does: its drain every ReceiveEvery finds the
// prekey due, and the renewal is an event. The mailbox's clock and the server's
// are two clocks.
func TestFollowRenewsAsReceiveDoes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := fakeserver.NewInMemory(t, time.Now)
		fast := func() time.Time { return time.Now().Add(2 * time.Second) }
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client(), Now: fast}
		b, err := client.Init(context.Background(), t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		client.Bounded(b)
		before, err := b.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var renewed []uint64
		start := time.Now()
		err = b.Follow(context.Background(), client.FollowOptions{Until: start.Add(26 * time.Hour)}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRenewed {
				renewed = append(renewed, ev.Result.Renewed.Serial)
				if at := time.Since(start); at > 24*time.Hour+client.ReceiveEvery {
					t.Errorf("renewed at %s, more than one ReceiveEvery after it was due", at)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		after, err := b.Status(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(renewed) != 1 || renewed[0] != after.PrekeySerial || after.PrekeySerial == before.PrekeySerial {
			t.Fatalf("over 26 hours: renewed %v, serial %d, then %d", renewed, before.PrekeySerial, after.PrekeySerial)
		}
	})
}

// A running loop with a prekey lifetime short enough that an hour between drains
// would pass its renewal drains as often as RecvWithin says, and so renews it
// while senders still take it.
func TestFollowDrainsAsOftenAsAShortLifetimeNeeds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := fakeserver.NewInMemory(t, time.Now)
		fast := func() time.Time { return time.Now().Add(2 * time.Second) }
		life := client.MinPrekeyLifetime
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client(), Now: fast, PrekeyLifetime: life}
		b, err := client.Init(context.Background(), t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		client.Bounded(b)
		start := time.Now()
		var at []time.Duration
		err = b.Follow(context.Background(), client.FollowOptions{Until: start.Add(7 * time.Hour)}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRenewed {
				at = append(at, time.Since(start))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if due := client.RenewAt(life); len(at) != 1 || at[0] < due || at[0] > due+client.RecvWithin(life) {
			t.Fatalf("renewed at %v of a %v lifetime, want one from %v, within %v", at, life, due, client.RecvWithin(life))
		}
	})
}

// Between runs after each drain. While it returns true it runs again each poll
// interval, with no drain, but for one each ReceiveEvery, which a renewal needs.
func TestBetweenBoundsTheHoldAndADrainStillFollowsEachHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		var between, drains atomic.Int32
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Minute)
		defer cancel()
		err := b.Follow(ctx, client.FollowOptions{Between: func(context.Context) bool {
			between.Add(1)
			return true
		}}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventDrained {
				drains.Add(1)
			}
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 150*time.Minute {
			t.Fatalf("Follow: %v after %s", err, time.Since(start))
		}
		if n := between.Load(); n < 140 || n > 151 {
			t.Errorf("Between ran %d times in 150 minutes at the fake's minute", n)
		}
		if n := drains.Load(); n != 3 {
			t.Errorf("%d drains in 150 minutes, want one at the start and one each hour", n)
		}
	})
}

// With Owed unset a drain records nothing owed, and Receive never does.
func TestOnlyAnOwedLoopRecordsAMessageOwed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "by Receive")
	receive(t, b)
	send(t, a, b, "by a loop")
	var owed int
	err := b.Follow(context.Background(), oneDrain(false), func(ctx context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			st, err := b.Status(ctx)
			if err != nil {
				return err
			}
			owed += st.Owed
		}
		return nil
	})
	if err != nil || owed != 0 {
		t.Fatalf("Follow: %v, with %d owed", err, owed)
	}
}

// A message is kept, and recorded owed, before its handler is handed it, and the
// record is cleared once the handler returned.
func TestAnOwedMessageIsKeptBeforeItsHandlerAndClearedAfter(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "kept first")
	handed := 0
	err := b.Follow(context.Background(), oneDrain(true), func(ctx context.Context, ev client.Event) error {
		if ev.Kind != client.EventDelivery {
			return nil
		}
		handed++
		st, err := b.Status(ctx)
		if err != nil {
			return err
		}
		if st.Owed != 1 || !exists(filepath.Join(b.Dir(), "mail", "inbox", ev.Delivery.ID+".json")) {
			t.Errorf("in the handler: %d owed; inbox/ %v", st.Owed, inbox(t, b))
		}
		return nil
	})
	if err != nil || handed != 1 {
		t.Fatalf("Follow: %v, having handed over %d", err, handed)
	}
	if st, err := b.Status(context.Background()); err != nil || st.Owed != 0 {
		t.Fatalf("after: %+v, %v", st, err)
	}
}

// Any other error from a handler leaves the message owed. The next loop hands
// over what is owed first, in delivery order, then what it drains.
func TestAnOwedMessageIsHandedOverFirstInDeliveryOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	var first []string
	for _, body := range []string{"one", "two", "three"} {
		send(t, a, b, body)
	}
	boom := errors.New("boom")
	err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			first = append(first, bodyOf(t, *ev.Delivery))
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) || len(first) != 3 {
		t.Fatalf("Follow: %v, having handed over %q", err, first)
	}
	if st, err := b.Status(context.Background()); err != nil || st.Owed != 3 {
		t.Fatalf("after: %+v, %v", st, err)
	}
	send(t, a, b, "new")
	var again []string
	err = b.Follow(context.Background(), oneDrain(true), func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			again = append(again, bodyOf(t, *ev.Delivery))
		}
		return nil
	})
	if err != nil || !slices.Equal(again, append(slices.Clone(first), "new")) {
		t.Fatalf("Follow: %v, handed over %q after %q", err, again, first)
	}
	if st, err := b.Status(context.Background()); err != nil || st.Owed != 0 {
		t.Fatalf("after: %+v, %v", st, err)
	}
}

// ErrGaveUp clears the record, the loop goes on to the next message, nothing
// hands it over again, and Status lists it under attention until its file in
// inbox/ is deleted.
func TestAMessageWhoseHandlingGaveUpIsListedAndNotTriedAgain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "gives up")
	send(t, a, b, "goes on")
	var gaveUp string
	var got []string
	err := b.Follow(ctx, oneDrain(true), func(_ context.Context, ev client.Event) error {
		if ev.Kind != client.EventDelivery {
			return nil
		}
		got = append(got, bodyOf(t, *ev.Delivery))
		if len(got) == 1 {
			gaveUp = ev.Delivery.ID
			return client.ErrGaveUp
		}
		return nil
	})
	if err != nil || len(got) != 2 {
		t.Fatalf("Follow: %v, having handed over %q", err, got)
	}
	st, err := b.Status(ctx)
	if err != nil || st.Owed != 0 || !slices.Equal(st.GaveUp, []string{gaveUp}) || !slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, gaveUp) }) {
		t.Fatalf("after: %+v, %v", st, err)
	}
	if err := b.Follow(ctx, oneDrain(true), func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery {
			t.Errorf("handed over again: %q", bodyOf(t, *ev.Delivery))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(b.Dir(), "mail", "inbox", gaveUp+".json")); err != nil {
		t.Fatal(err)
	}
	if st, err = b.Status(ctx); err != nil || len(st.GaveUp) != 0 || slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, gaveUp) }) {
		t.Fatalf("its file deleted: %+v, %v", st, err)
	}
	if err := b.Follow(ctx, oneDrain(true), nil); err != nil {
		t.Fatal(err)
	}
	if _, kept := b.Record(t, "gave-up/"+gaveUp); kept {
		t.Fatal("the next owed loop keeps the record of a message inbox/ no longer holds")
	}
}

// A message whose handling gave up stays listed while inbox/ keeps its file, a
// later owed loop included. ClearFailed clears every record in one call, counts
// those Status listed, drops the record of one inbox/ no longer keeps, and leaves
// every file in inbox/; a handling that gives up after it is listed again.
func TestClearFailedClearsEveryHandlingThatGaveUpUntilTheNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "one")
	send(t, a, b, "two")
	send(t, a, b, "three")
	var gaveUp []string
	giveUp := func(_ context.Context, ev client.Event) error {
		if ev.Kind != client.EventDelivery {
			return nil
		}
		gaveUp = append(gaveUp, ev.Delivery.ID)
		return client.ErrGaveUp
	}
	if err := b.Follow(ctx, oneDrain(true), giveUp); err != nil || len(gaveUp) != 3 {
		t.Fatalf("Follow: %v, gave up on %q", err, gaveUp)
	}
	if err := b.Follow(ctx, oneDrain(true), nil); err != nil {
		t.Fatal(err)
	}
	listed := func(ids []string) {
		t.Helper()
		st, err := b.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(ids)
		got := slices.Sorted(slices.Values(st.GaveUp))
		said := slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, "callback gave up") })
		if !slices.Equal(got, ids) || said != (len(ids) > 0) {
			t.Fatalf("status lists %q, says %v, want %q: %q", got, said, ids, st.Attention)
		}
	}
	listed(slices.Clone(gaveUp))
	// A file deleted, with no owed loop since, leaves its record in the state.
	gone := gaveUp[2]
	if err := os.Remove(filepath.Join(b.Dir(), "mail", "inbox", gone+".json")); err != nil {
		t.Fatal(err)
	}
	if _, kept := b.Record(t, "gave-up/"+gone); !kept {
		t.Fatal("no record left of the message whose file was deleted: the test proves nothing of it")
	}
	n, err := b.ClearFailed(ctx)
	if err != nil || n != 2 {
		t.Fatalf("ClearFailed: %d, %v, want the 2 status listed", n, err)
	}
	for _, id := range gaveUp {
		if _, kept := b.Record(t, "gave-up/"+id); kept {
			t.Errorf("ClearFailed left the record of %s", id)
		}
	}
	listed(nil)
	if ds, err := b.Inbox(ctx); err != nil || len(ds) != 2 {
		t.Fatalf("inbox/ after ClearFailed: %d messages, %v, want the 2 it kept", len(ds), err)
	}
	if n, err := b.ClearFailed(ctx); err != nil || n != 0 {
		t.Fatalf("ClearFailed with nothing to clear: %d, %v", n, err)
	}
	send(t, a, b, "four")
	gaveUp = nil
	if err := b.Follow(ctx, oneDrain(true), giveUp); err != nil || len(gaveUp) != 1 {
		t.Fatalf("Follow: %v, gave up on %q", err, gaveUp)
	}
	listed(slices.Clone(gaveUp))
}

// An owed message inbox/ no longer keeps as recv wrote it is said and cleared,
// and its handler is not handed anything to read.
func TestAnOwedMessageGoneFromInboxIsSaidAndCleared(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		do   func(path string) error
	}{
		{"deleted", os.Remove},
		{"written by something else", func(path string) error { return os.WriteFile(path, []byte(`{"body":"not recv's line"}`), 0o600) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			send(t, a, b, "goes")
			var id string
			boom := errors.New("boom")
			if err := b.Follow(ctx, oneDrain(true), func(_ context.Context, ev client.Event) error {
				if ev.Kind == client.EventDelivery {
					id = ev.Delivery.ID
					return boom
				}
				return nil
			}); !errors.Is(err, boom) {
				t.Fatal(err)
			}
			if err := c.do(filepath.Join(b.Dir(), "mail", "inbox", id+".json")); err != nil {
				t.Fatal(err)
			}
			var gone []string
			if err := b.Follow(ctx, oneDrain(true), func(_ context.Context, ev client.Event) error {
				switch ev.Kind {
				case client.EventDelivery:
					t.Error("the handler was handed a message inbox/ does not keep")
				case client.EventOwedGone:
					gone = append(gone, ev.ID)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if st, err := b.Status(ctx); err != nil || st.Owed != 0 || !slices.Equal(gone, []string{id}) {
				t.Fatalf("said %v of %s; %+v, %v", gone, id, st, err)
			}
		})
	}
}

// A server that forgets the mailbox while its list is held answers the hold
// mailbox_not_found: the hold is Changed, and the loop's next drain registers the
// mailbox again and goes on, so a message sent after is delivered (#773).
func TestAMailboxForgottenWhileItsListIsHeldIsRegisteredAgainByTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		var forgot atomic.Bool
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") && forgot.CompareAndSwap(false, true) {
				e.srv.Forget(b.Address())
			}
			next.ServeHTTP(w, r)
		})
		var renewed bool
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventRenewed:
				renewed = true
			case client.EventDrained:
				if renewed && len(got) == 0 {
					send(t, a, b, "after the registration")
				}
			case client.EventDelivery:
				got = append(got, bodyOf(t, *ev.Delivery))
				return client.ErrStop
			}
			return nil
		})
		if err != nil || !forgot.Load() || !renewed || !slices.Equal(got, []string{"after the registration"}) {
			t.Fatalf("Follow: %v; forgotten %v, renewed %v, delivered %q", err, forgot.Load(), renewed, got)
		}
	})
}

// A held list answered mailbox_not_found is Changed, as one whose pass is
// refused, for the Receive that registers the mailbox again.
func TestAHoldItsServerHoldsNoMailboxForIsChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		b := e.mailbox()
		receive(t, b)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") {
				forge.Refuse(w, protocol.CodeMailboxNotFound)
				return
			}
			next.ServeHTTP(w, r)
		})
		if h, err := b.Hold(context.Background(), time.Hour); err != nil || !h.Changed {
			t.Fatalf("a hold answered mailbox_not_found came to %+v, %v; want changed", h, err)
		}
	})
}

// A drain answered mailbox_not_found whose registration again fails in a way a
// wait may mend (a 503 or a 429) is an EventRetry, not Fatal: in the loop that
// met it, and in a loop started again while the renewal is held back
// (renewRetry). Once the hour has passed, a drain registers the mailbox again
// (#773).
func TestANoSuchMailboxWhoseRegistrationFailsForNowIsARetry(t *testing.T) {
	t.Parallel()
	for _, refusal := range []protocol.Code{protocol.CodeUnavailable, protocol.CodeRateLimited} {
		t.Run(string(refusal), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			e := newEnv(t)
			b := e.mailbox()
			receive(t, b)
			e.srv.Forget(b.Address())
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if r.Method != http.MethodGet {
					forge.Refuse(w, refusal)
					return
				}
				next.ServeHTTP(w, r)
			})
			retry := func(b *client.Mailbox) error {
				var retried error
				if err := b.Follow(ctx, client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
					if ev.Kind == client.EventRetry {
						retried = ev.Err
						return client.ErrStop
					}
					return nil
				}); err != nil {
					return err
				}
				return retried
			}
			for _, run := range []string{"the loop that met it", "a loop started again"} {
				err := retry(mustOpen(t, b.Dir(), e.opts()))
				if client.Code(err) != protocol.CodeMailboxNotFound || !client.Transient(err) || client.Fatal(err) {
					t.Fatalf("%s: %v; Transient %v, Fatal %v", run, err, client.Transient(err), client.Fatal(err))
				}
			}
			if err := b.Follow(ctx, oneDrain(false), nil); err == nil || client.Fatal(err) {
				t.Fatalf("one drain within the hour: %v", err)
			}
			e.srv.SetMiddle(nil)
			e.clock.Add(61 * time.Minute)
			if err := b.Follow(ctx, oneDrain(false), nil); err != nil {
				t.Fatalf("one drain an hour later: %v", err)
			}
			if _, err := b.Receive(ctx); err != nil {
				t.Fatalf("the drain after: %v", err)
			}
		})
	}
}

// A drain answered mailbox_not_found whose registration again is refused for
// good ends the loop, as Fatal.
func TestANoSuchMailboxWhoseRegistrationIsRefusedIsFatal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	receive(t, b)
	e.srv.Forget(b.Address())
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method != http.MethodGet {
			forge.Refuse(w, protocol.CodeInvalidRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
	err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventRetry {
			t.Errorf("retried: %v", ev.Err)
			return client.ErrStop
		}
		return nil
	})
	if client.Code(err) != protocol.CodeMailboxNotFound || !client.Fatal(err) {
		t.Fatalf("Follow: %v", err)
	}
}

// A folder of the mailbox missing, as on a volume not yet mounted, is NotReady:
// the loop tries again ReceiveEvery after each refusal, and drains once the
// folder is back. Await, which meets it at a hold, waits until it returns for a
// Receive (#775 item 2).
func TestAFolderNotYetMountedIsTriedAgainEveryHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "waits for the mount")
		inbox := b.Path("inbox")
		if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		var at []time.Duration
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventRetry:
				if !client.NotReady(ev.Err) || !client.Transient(ev.Err) || client.Fatal(ev.Err) || !strings.Contains(ev.Err.Error(), inbox) {
					t.Errorf("retried %v; NotReady %v", ev.Err, client.NotReady(ev.Err))
				}
				if at = append(at, time.Since(start)); len(at) == 2 {
					if err := os.Rename(inbox+".unmounted", inbox); err != nil {
						t.Error(err)
					}
				}
			case client.EventDelivery:
				got = append(got, bodyOf(t, *ev.Delivery))
				return client.ErrStop
			}
			return nil
		})
		if err != nil || !slices.Equal(got, []string{"waits for the mount"}) || time.Since(start) != 2*client.ReceiveEvery {
			t.Fatalf("Follow: %v, delivering %q after %s", err, got, time.Since(start))
		}
		if want := []time.Duration{0, client.ReceiveEvery}; !slices.Equal(at, want) {
			t.Fatalf("tried at %v, want %v", at, want)
		}
		if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		retries := 0
		err = b.Await(context.Background(), start.Add(3*client.ReceiveEvery), func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRetry {
				retries++
			}
			return nil
		})
		if err != nil || retries != 1 || time.Since(start) != client.ReceiveEvery {
			t.Fatalf("Await: %v after %s, having retried %d", err, time.Since(start), retries)
		}
	})
}

// A one-shot drain that meets a folder missing ends with the refusal, which is
// NotReady and not Fatal.
func TestAOneShotDrainOfAFolderNotMountedIsNotFatal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	receive(t, b)
	keys := filepath.Join(b.Dir(), "keys")
	if err := os.Rename(keys, keys+".unmounted"); err != nil {
		t.Fatal(err)
	}
	err := b.Follow(context.Background(), oneDrain(false), nil)
	if err == nil || !client.NotReady(err) || client.Fatal(err) {
		t.Fatalf("Follow with keys/ not mounted: %v", err)
	}
	if err := os.Rename(keys+".unmounted", keys); err != nil {
		t.Fatal(err)
	}
	if err := b.Follow(context.Background(), oneDrain(false), nil); err != nil {
		t.Fatalf("Follow once mounted: %v", err)
	}
}

// A clock three hours behind, as at a boot before a time source sets it, is
// NotReady: for a mailbox whose identity signs, its server refuses the pass as
// pass_expired and dates its answer later than the clock reads; under a
// certificate, the certificate has not begun. Either is an EventRetry the loop
// makes again ReceiveEvery later, and once the clock is right the loop drains.
// The mailbox's clock and the server's are two clocks (#775 item 2).
func TestAClockBehindIsTriedAgainEveryHour(t *testing.T) {
	for _, working := range []bool{false, true} {
		t.Run(fmt.Sprintf("working key %v", working), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := newMemEnv(t)
				e.working = working
				b := e.mailbox()
				receive(t, b)
				var behind atomic.Bool
				behind.Store(true)
				o := e.opts()
				o.Now = func() time.Time {
					if behind.Load() {
						return e.clock.Now().Add(-3 * time.Hour)
					}
					return e.clock.Now()
				}
				x := mustOpen(t, b.Dir(), o)
				client.Bounded(x)
				start := time.Now()
				var at []time.Duration
				err := x.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
					switch ev.Kind {
					case client.EventRetry:
						if !client.NotReady(ev.Err) || client.Fatal(ev.Err) {
							t.Errorf("retried %v; NotReady %v", ev.Err, client.NotReady(ev.Err))
						}
						if at = append(at, time.Since(start)); len(at) == 2 {
							behind.Store(false)
						}
					case client.EventDrained:
						if len(at) == 2 {
							return client.ErrStop
						}
					}
					return nil
				})
				if want := []time.Duration{0, client.ReceiveEvery}; err != nil || !slices.Equal(at, want) {
					t.Fatalf("Follow: %v, having tried at %v; want nil, at %v", err, at, want)
				}
			})
		})
	}
}

// A hold whose pass its server refuses as certificate_revoked, where the
// revocation verifies and covers the certificate in use, is Changed: the
// Receive after renews under a new certificate (renewalMends; #800).
func TestAHoldWhoseCertificateItsServerShowsRevokedIsChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		e.working = true
		b := e.mailbox()
		receive(t, b)
		publish(t, e, certsRecord(t, b.Dir(), numberOf(t, b)))
		h, err := b.Hold(context.Background(), time.Hour)
		if err != nil || !h.Changed {
			t.Fatalf("a hold answered certificate_revoked came to %+v, %v; want changed", h, err)
		}
		if res, err := b.Receive(context.Background()); err != nil || res.Renewed == nil {
			t.Fatalf("the Receive after: %+v, %v; want a renewal", res, err)
		}
	})
}

// A revocation of the certificate in use, published while the loop holds the
// list, is renewed past by the loop, which goes on delivering.
func TestACertificateRevokedWhileItsListIsHeldIsRenewedByTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		e.working = true
		b := e.mailbox()
		e.working = false
		a := e.mailbox()
		receive(t, b)
		rec := certsRecord(t, b.Dir(), numberOf(t, b))
		var published atomic.Bool
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.URL.Query().Has("wait") && published.CompareAndSwap(false, true) {
				if _, err := client.Publish(context.Background(), e.opts(), rec); err != nil {
					t.Error(err)
				}
			}
			next.ServeHTTP(w, r)
		})
		var renewed bool
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventRenewed:
				renewed = true
			case client.EventDrained:
				if renewed && len(got) == 0 {
					send(t, a, b, "after the renewal")
				}
			case client.EventDelivery:
				got = append(got, bodyOf(t, *ev.Delivery))
				return client.ErrStop
			case client.EventRetry:
				t.Errorf("retried: %v", ev.Err)
			}
			return nil
		})
		if err != nil || !published.Load() || !renewed || !slices.Equal(got, []string{"after the renewal"}) {
			t.Fatalf("Follow: %v; published %v, renewed %v, delivered %q", err, published.Load(), renewed, got)
		}
	})
}

// A drain refused because its server shows the certificate in use revoked,
// whose renewal under a new certificate fails in a way a wait may mend, is an
// EventRetry, not Fatal: in the loop that met it, and in a loop started again
// while the renewal is held back (renewRetry). Once the hour has passed, a
// drain renews (#800).
func TestACertificateRevokedWhoseRenewalFailsForNowIsARetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	b := e.mailbox()
	receive(t, b)
	publish(t, e, certsRecord(t, b.Dir(), numberOf(t, b)))
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/prekey") {
			forge.Refuse(w, protocol.CodeUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
	for _, run := range []string{"the loop that met it", "a loop started again"} {
		var retried error
		err := mustOpen(t, b.Dir(), e.opts()).Follow(ctx, client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventRetry {
				retried = ev.Err
				return client.ErrStop
			}
			return nil
		})
		if err != nil || retried == nil || !client.Transient(retried) || client.Fatal(retried) {
			t.Fatalf("%s: %v, retried %v; Transient %v, Fatal %v", run, err, retried, client.Transient(retried), client.Fatal(retried))
		}
	}
	e.srv.SetMiddle(nil)
	e.clock.Add(61 * time.Minute)
	if err := mustOpen(t, b.Dir(), e.opts()).Follow(ctx, oneDrain(false), nil); err != nil {
		t.Fatalf("one drain an hour later: %v", err)
	}
}

// The owner's cert revoke-earlier while the loop holds the list, its renewal
// refused for now: the loop's next hold meets the revocation, and the drain
// after renews, as RenewAndRevoke says Receive does.
func TestRevokeEarlierWhileTheLoopHoldsIsRenewedByTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		e.working = true
		b := e.mailbox()
		receive(t, b)
		var refusing atomic.Bool
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if refusing.Load() && r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/prekey") {
				forge.Refuse(w, protocol.CodeUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		drains, revoked := 0, false
		err := b.Follow(ctx, client.FollowOptions{}, func(ctx context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventDrained:
				drains++
				if drains == 1 {
					refusing.Store(true)
					rem, err := b.RenewAndRevoke(ctx)
					refusing.Store(false)
					if err != nil || rem.Revoked == nil || rem.NotRenewed == nil {
						t.Errorf("cert revoke-earlier, its renewal refused: %+v, %v", rem, err)
					}
					revoked = rem.Revoked != nil
				}
			case client.EventRenewed:
				return client.ErrStop
			}
			return nil
		})
		if err != nil || !revoked || drains < 2 {
			t.Fatalf("Follow: %v after %d drains; revoked %v", err, drains, revoked)
		}
	})
}

// Under FollowOptions.Owed, a folder of the mailbox missing as the loop
// begins, as on a volume not yet mounted, is tried again ReceiveEvery after
// each refusal, as a drain's is, and the loop delivers once the folder is back
// (#801).
func TestAnOwedHandoverRidesOutAFolderNotYetMounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "waits for the mount")
		inbox := b.Path("inbox")
		if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		var at []time.Duration
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventRetry:
				if !client.NotReady(ev.Err) || client.Fatal(ev.Err) {
					t.Errorf("retried %v; NotReady %v", ev.Err, client.NotReady(ev.Err))
				}
				if at = append(at, time.Since(start)); len(at) == 2 {
					if err := os.Rename(inbox+".unmounted", inbox); err != nil {
						t.Error(err)
					}
				}
			case client.EventDelivery:
				got = append(got, bodyOf(t, *ev.Delivery))
				return client.ErrStop
			}
			return nil
		})
		if err != nil || !slices.Equal(got, []string{"waits for the mount"}) {
			t.Fatalf("Follow: %v, delivering %q after %s", err, got, time.Since(start))
		}
		if want := []time.Duration{0, client.ReceiveEvery}; !slices.Equal(at, want) {
			t.Fatalf("tried at %v, want %v", at, want)
		}
	})
}

// Under FollowOptions.Owed, a folder that goes missing while the handler runs
// leaves the message owed, and the drain's deliveries after it are not handed
// over: the loop tries again ReceiveEvery after, hands each over once the
// folder is back, in delivery order, and clears them then (#801).
func TestAFolderGoneDuringAnOwedHandlingHandsTheMessageOverAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "one")
		send(t, a, b, "two")
		inbox := b.Path("inbox")
		start := time.Now()
		var at []time.Duration
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventDelivery:
				switch got = append(got, bodyOf(t, *ev.Delivery)); len(got) {
				case 1:
					if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
						t.Error(err)
					}
				case 3:
					return client.ErrStop
				}
			case client.EventRetry:
				if !client.NotReady(ev.Err) || client.Fatal(ev.Err) {
					t.Errorf("retried %v; NotReady %v", ev.Err, client.NotReady(ev.Err))
				}
				if at = append(at, time.Since(start)); len(at) == 2 {
					if err := os.Rename(inbox+".unmounted", inbox); err != nil {
						t.Error(err)
					}
				}
			}
			return nil
		})
		// The first delivery is handed over again, and the one after it, which
		// the fault left owed, only once the folder is back, in delivery order.
		if err != nil || len(got) != 3 || got[0] != got[1] || got[2] == got[0] || !slices.Equal(slices.Sorted(slices.Values(got[1:])), []string{"one", "two"}) {
			t.Fatalf("Follow: %v, handing over %q after %s", err, got, time.Since(start))
		}
		if want := []time.Duration{0, client.ReceiveEvery}; !slices.Equal(at, want) {
			t.Fatalf("tried at %v, want %v", at, want)
		}
		owed := 0
		if err := b.Follow(context.Background(), client.FollowOptions{Owed: true, Until: time.Now()}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventDelivery {
				owed++
			}
			return nil
		}); err != nil || owed != 0 {
			t.Fatalf("the loop after: %v, handing over %d", err, owed)
		}
	})
}

// A read of a sender's revocations answered with a wait (a 429 with its
// Retry-After, or a 503) is a call to make again: the drain's error keeps the
// server's answer, the loop waits the Retry-After and delivers after it, and
// nothing renews the reader (#804).
func TestAWaitAboutASenderIsTheLoopsRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "after the wait")
		var refused atomic.Int32
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if x, ok := forge.RevocationsOf(r); ok && x == a.Address() && refused.Add(1) == 1 {
				c := protocol.CodeRateLimited
				w.Header().Set("Retry-After", "3600")
				fakeserver.WriteJSON(w, c.Status(), protocol.Problem{Type: c.TypeURI(), Code: c, Status: int64(c.Status()), QuotaID: protocol.QuotaMisses, Subject: protocol.SubjectIP})
				return
			}
			next.ServeHTTP(w, r)
		})
		res, err := b.Receive(context.Background())
		var api *client.APIError
		if !errors.As(err, &api) || api.Problem.Code != protocol.CodeRateLimited || api.RetryAfter != time.Hour || !client.Transient(err) || client.Fatal(err) {
			t.Fatalf("Receive: %v; want the 429 with its hour", err)
		}
		if res.Renewed != nil || res.RenewErr != nil {
			t.Fatalf("Receive renewed on a refusal about the sender: %+v", res)
		}
		if got := client.PauseAfter(res, err); got != time.Hour {
			t.Fatalf("the loop would drain again after %v, want the hour", got)
		}
		refused.Store(0)
		start := time.Now()
		var got []string
		err = b.Follow(context.Background(), client.FollowOptions{}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventRenewed:
				t.Error("renewed on a refusal about the sender")
			case client.EventDelivery:
				got = append(got, bodyOf(t, *ev.Delivery))
				return client.ErrStop
			}
			return nil
		})
		if err != nil || !slices.Equal(got, []string{"after the wait"}) || time.Since(start) != time.Hour {
			t.Fatalf("Follow: %v, delivering %q after %s", err, got, time.Since(start))
		}
	})
}

// Under FollowOptions.Owed, a folder that goes missing while the handler runs
// on a message owed from an earlier loop leaves it owed: the loop tries again
// ReceiveEvery after, and hands it over again once the folder is back (#801).
func TestAFolderGoneDuringTheHandoverOfWhatIsOwedHandsItOverAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newMemEnv(t)
		a, b := e.mailbox(), e.mailbox()
		receive(t, b)
		send(t, a, b, "owed")
		failed := errors.New("the handler failed")
		if err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventDelivery {
				return failed
			}
			return nil
		}); !errors.Is(err, failed) {
			t.Fatalf("the loop that left it owed: %v", err)
		}
		inbox := b.Path("inbox")
		start := time.Now()
		var at []time.Duration
		var got []string
		err := b.Follow(context.Background(), client.FollowOptions{Owed: true}, func(_ context.Context, ev client.Event) error {
			switch ev.Kind {
			case client.EventDelivery:
				if got = append(got, bodyOf(t, *ev.Delivery)); len(got) == 1 {
					if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
						t.Error(err)
					}
					return nil
				}
				return client.ErrStop
			case client.EventRetry:
				if !client.NotReady(ev.Err) || client.Fatal(ev.Err) {
					t.Errorf("retried %v; NotReady %v", ev.Err, client.NotReady(ev.Err))
				}
				if at = append(at, time.Since(start)); len(at) == 2 {
					if err := os.Rename(inbox+".unmounted", inbox); err != nil {
						t.Error(err)
					}
				}
			}
			return nil
		})
		if err != nil || !slices.Equal(got, []string{"owed", "owed"}) {
			t.Fatalf("Follow: %v, handing over %q after %s", err, got, time.Since(start))
		}
		if want := []time.Duration{0, client.ReceiveEvery}; !slices.Equal(at, want) {
			t.Fatalf("tried at %v, want %v", at, want)
		}
	})
}
