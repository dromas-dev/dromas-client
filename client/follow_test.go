package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	if !slices.Equal(seen, []string{"delivered", client.RefusedDecrypt, "held"}) {
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
					forge.Refuse(w, protocol.CodeBadRequest)
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
		if !client.Fatal(err) || client.Code(err) != protocol.CodeBadRequest || len(retried) != 1 || client.Fatal(retried[0]) {
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
	for _, c := range []struct {
		name             string
		err              error
		fatal, transient bool
	}{
		{"no error", nil, false, false},
		{"a revocation", &client.RevokedError{}, true, false},
		{"another format", &client.FormatError{}, true, false},
		{"a client's refusal", &client.RefusedError{Reason: "no"}, true, false},
		{"no mailbox", apiError(http.StatusNotFound, protocol.CodeNoSuchMailbox), true, false},
		{"bad request", apiError(http.StatusBadRequest, protocol.CodeBadRequest), true, false},
		{"a server error", apiError(http.StatusServiceUnavailable, protocol.CodeUnavailable), false, true},
		{"a rate limit", apiError(http.StatusTooManyRequests, protocol.CodeRateLimited), false, true},
		{"the lock", client.ErrLocked, false, true},
		{"a fault of the disk", errors.New("no space left on device"), false, false},
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
