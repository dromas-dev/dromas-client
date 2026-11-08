package client

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// EventKind says what an [Event] is.
type EventKind int

// The kinds of [Event]. A drain hands over its events in this order, from
// [EventDelivery] to [EventDrained], and the order among a kind's events is the
// drain's.
const (
	// EventDelivery is a message that landed in inbox/: [Event.Delivery].
	EventDelivery EventKind = iota + 1
	// EventDuplicate is a retry of a message already delivered: [Event.ID].
	EventDuplicate
	// EventRefusal is a message refused and kept in refused/: [Event.Refusal].
	EventRefusal
	// EventHeld is a message kept in held/: [Event.Delivery].
	EventHeld
	// EventBlocked is a message from a sender the mailbox blocked, dropped:
	// [Event.ID].
	EventBlocked
	// EventMore is a drain that stopped at a limit, or that the server paced:
	// [Result.More], [Result.Paced].
	EventMore
	// EventRenewed is a prekey published: [Result.Renewed].
	EventRenewed
	// EventRenewFailed is a due renewal that failed: [Event.Err].
	EventRenewFailed
	// EventNotMade is a statement the mailbox did not make: [Result.NotMade].
	EventNotMade
	// EventACLFailed is the mailbox's ACL not sent: [Event.Err].
	EventACLFailed
	// EventACLReplaced is an ACL another writer put at the mailbox's server, which
	// the mailbox replaced.
	EventACLReplaced
	// EventStaleAfterRestore is the drain's refusals of messages sent before the
	// mailbox's restore (Options.Restore):
	// [Result.RefusedBeforeRestore].
	EventStaleAfterRestore
	// EventDrained ends each drain's events, and carries its [Result].
	EventDrained
	// EventConnectionDropped is a connection that dropped while the list was
	// held: [Event.Hold].
	EventConnectionDropped
	// EventRetry is a failed drain or held request that the loop makes again:
	// [Event.Err].
	EventRetry
	// EventOwedGone is an owed message that inbox/ no longer keeps as recv wrote
	// it, so the handler is not handed it: [Event.ID].
	EventOwedGone
)

// Event is one thing a mailbox's receive-and-hold loop met.
type Event struct {
	Kind EventKind
	// Delivery is the message of an EventDelivery or an EventHeld, kept in its
	// folder before the handler sees it.
	Delivery *Delivery
	// ID is the id of the message of an EventDuplicate, an EventBlocked or an
	// EventOwedGone.
	ID string
	// Refusal is the message of an EventRefusal.
	Refusal *Refusal
	// Hold is what the held list of an EventConnectionDropped came to.
	Hold *HoldResult
	// Err is why, in an EventRenewFailed, an EventACLFailed or an EventRetry.
	Err error
	// Result is the drain an event belongs to, in every event a drain hands
	// over, and nil otherwise.
	Result *Result
}

// Handler is what [Mailbox.Follow] and [Mailbox.Await] hand each [Event] to, one at
// a time and with the mailbox's lock free, so that a handler may run any
// command of the mailbox. A panic in a handler is not caught.
type Handler func(ctx context.Context, e Event) error

func noHandler(context.Context, Event) error { return nil }

// ErrStop is what a [Handler] returns to end [Mailbox.Follow] with no error.
var ErrStop = errors.New("the handler stopped the loop")

// ErrGaveUp is what a [Handler] returns where [FollowOptions.Owed] is set, for a
// delivery whose handling failed for good. [Mailbox.Follow] records it, for
// [Mailbox.Status] to list under attention until [Mailbox.ClearFailed] clears
// every such record, and goes on: nothing tries it again.
var ErrGaveUp = errors.New("the handler gave up on the message")

// FollowOptions says how [Mailbox.Follow] runs. The zero value runs until its
// context ends.
type FollowOptions struct {
	// Until is when the loop ends, after one last drain. Zero is never, and a
	// time that has passed is one drain.
	Until time.Time
	// Between, when not nil, is the caller's own work: it runs after each
	// drain's events, and again each time a hold it bounded runs out. A true
	// return bounds the next hold at the poll interval, so that Between runs
	// again within that interval, with no drain between unless the list changes.
	// A drain still follows at [ReceiveEvery], or sooner as a short prekey
	// lifetime needs ([RecvWithin]).
	Between func(ctx context.Context) bool
	// Owed makes the handler's work on each delivery safe against a crash. The
	// transaction that records a message landed in inbox/ as seen also records the
	// message as owed its handling, and the record is cleared once the handler
	// returns nil, [ErrStop] or [ErrGaveUp]. Follow begins by handing over what
	// is owed, in delivery order, and the handler's work is at least once: a
	// crash before the record is cleared hands the message over again. Any
	// other error from the handler leaves the message owed.
	Owed bool
}

// Follow is the mailbox's receive-and-hold loop: it drains, hands the handler
// each [Event] of what the drain did, and holds the list until a message
// arrives (Await), or until the next drain is due to renew the prekey as Receive
// does. It goes on until its context ends, until [FollowOptions.Until]
// passes, until the handler returns an error, or until a drain fails in a way
// no wait mends.
//
// The lock is held only as Receive and Hold hold it: never across a hold, and
// never while the handler runs. A message is kept in its folder before the
// handler sees it, and a handler that ends the loop on a delivery still sees
// the rest of that drain's events, because the drain has acked those messages.
// A handler's error ends the loop after the last of them.
//
// A drain that fails in a way a wait may mend ([Transient]) is an [EventRetry],
// a no_such_mailbox among them where the renewal that registers the mailbox
// again failed so ([Result.RenewErr]), and the loop drains again after the
// error's Retry-After, or the later of the server's poll interval and its pace,
// or a minute, or [ReceiveEvery] after a refusal this machine puts right without
// its owner ([NotReady]). Where that lands past Until
// the drain's error ends the loop, as any other drain's error does. Follow returns
// that error, the handler's, ctx's when ctx ended, and nil for [ErrStop] or at Until.
func (b *Mailbox) Follow(ctx context.Context, o FollowOptions, h Handler) error {
	if h == nil {
		h = noHandler
	}
	between := o.Between
	if between == nil {
		between = func(context.Context) bool { return false }
	}
	var stopped error
	if o.Owed {
		var err error
		if stopped, err = b.handOverOwed(ctx, h); err != nil {
			return err
		}
	}
	for {
		res, err := locked(ctx, b, func() (*Result, error) { return b.receive(ctx, o.Owed) })
		if renewalMends(code(err)) && res != nil && Transient(res.RenewErr) {
			err = &notRegistered{err}
		}
		if serr := b.handOver(ctx, o.Owed, res, h); serr != nil && stopped == nil {
			stopped = serr
		}
		pause := pauseAfter(res, err)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case stopped == nil && Transient(err) && (o.Until.IsZero() || time.Until(o.Until) > pause):
			if herr := h(ctx, Event{Kind: EventRetry, Err: err}); herr != nil {
				return ended(herr)
			}
		case err != nil:
			return err
		case stopped != nil:
			return ended(stopped)
		case !o.Until.IsZero() && !time.Now().Before(o.Until):
			return nil
		}
		again := between(ctx)
		if err == nil && !res.More && res.Paced == 0 {
			if err := b.holdNext(ctx, o, between, again, pause, time.Now().Add(drainWithin(res)), h); err != nil {
				return ended(err)
			}
			continue
		}
		if !o.Until.IsZero() {
			pause = min(pause, time.Until(o.Until))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
	}
}

// notRegistered is a drain's no_such_mailbox whose renewal, which registers the
// mailbox again, failed in a way a wait may mend, or is held back after such a
// failure ([Transient]): a later drain renews again.
type notRegistered struct{ err error }

func (e *notRegistered) Error() string {
	return e.err.Error() + "; registering this mailbox again failed in a way a wait may mend, and a later recv tries again"
}

func (e *notRegistered) Unwrap() error { return e.err }

// ended is what Follow returns for a handler's error.
func ended(err error) error {
	if errors.Is(err, ErrStop) {
		return nil
	}
	return err
}

// pauseAfter is how long the loop waits before the drain after res and err:
// ReceiveEvery after a refusal this machine puts right ([NotReady]), the
// longest the loop otherwise goes between drains.
func pauseAfter(res *Result, err error) time.Duration {
	if NotReady(err) {
		return ReceiveEvery
	}
	pause := time.Minute
	if res != nil && res.Again() > 0 {
		pause = res.Again()
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		pause = ae.RetryAfter
	}
	return pause
}

// holdNext holds the list until a drain is due: the list changed, Until came,
// or latest came. While again is true each hold ends at the pause, and between
// then runs with no drain.
func (b *Mailbox) holdNext(ctx context.Context, o FollowOptions, between func(context.Context) bool, again bool, pause time.Duration, latest time.Time, h Handler) error {
	for {
		until, bounded := latest, false
		if !o.Until.IsZero() && o.Until.Before(until) {
			until = o.Until
		}
		if end := time.Now().Add(pause); again && end.Before(until) {
			until, bounded = end, true
		}
		if err := b.Await(ctx, until, h); err != nil {
			return err
		}
		if !bounded || time.Now().Before(until) {
			return nil
		}
		again = between(ctx)
	}
}

// drainWithin is how soon after a drain the next must begin: at the
// ReceiveEvery Await keeps, or sooner where the prekey in use has a lifetime
// that needs it ([RecvWithin]), but never sooner than the poll budget's MinHold.
func drainWithin(res *Result) time.Duration {
	if res.receiveWithin > 0 {
		return min(ReceiveEvery, max(res.receiveWithin, MinHold))
	}
	return ReceiveEvery
}

// handOver hands h each event of one drain, res, and returns h's first error,
// or the first fault at clearing what was owed.
func (b *Mailbox) handOver(ctx context.Context, owed bool, res *Result, h Handler) error {
	if res == nil {
		return nil
	}
	var stopped error
	keep := func(err error) {
		if err != nil && stopped == nil {
			stopped = err
		}
	}
	give := func(e Event) {
		e.Result = res
		keep(h(ctx, e))
	}
	for i := range res.Delivered {
		d := &res.Delivered[i]
		if owed {
			keep(b.handOwed(ctx, d, res, h))
		} else {
			give(Event{Kind: EventDelivery, Delivery: d})
		}
	}
	for _, id := range res.Duplicates {
		give(Event{Kind: EventDuplicate, ID: id})
	}
	for i := range res.Refused {
		give(Event{Kind: EventRefusal, Refusal: &res.Refused[i]})
	}
	for i := range res.Held {
		give(Event{Kind: EventHeld, Delivery: &res.Held[i]})
	}
	for _, id := range res.Blocked {
		give(Event{Kind: EventBlocked, ID: id})
	}
	if res.More || res.Paced > 0 {
		give(Event{Kind: EventMore})
	}
	if res.Renewed != nil {
		give(Event{Kind: EventRenewed})
	}
	if res.RenewErr != nil {
		give(Event{Kind: EventRenewFailed, Err: res.RenewErr})
	}
	if res.NotMade != nil {
		give(Event{Kind: EventNotMade})
	}
	if res.ACLErr != nil {
		give(Event{Kind: EventACLFailed, Err: res.ACLErr})
	}
	if res.ACLReplaced {
		give(Event{Kind: EventACLReplaced})
	}
	if res.RefusedBeforeRestore() > 0 {
		give(Event{Kind: EventStaleAfterRestore})
	}
	give(Event{Kind: EventDrained})
	return stopped
}

// handOwed hands h a delivery that is owed its handling, and clears the record
// once the handling has ended: h returned nil, ErrStop or ErrGaveUp, which it
// records. It returns h's error, but for ErrGaveUp, which the loop goes on after.
func (b *Mailbox) handOwed(ctx context.Context, d *Delivery, res *Result, h Handler) error {
	step("handler begun")
	err := h(ctx, Event{Kind: EventDelivery, Delivery: d, Result: res})
	step("handler returned")
	gaveUp := errors.Is(err, ErrGaveUp)
	if err != nil && !gaveUp && !errors.Is(err, ErrStop) {
		return err
	}
	if cerr := b.clearOwed(ctx, d.owed, d.ID, gaveUp); cerr != nil {
		return cerr
	}
	step("owed cleared")
	if gaveUp {
		return nil
	}
	return err
}

// owedMessage is a message the state file records as owed its handling: the
// delivery inbox/ keeps, or none where inbox/ no longer keeps it as recv wrote it.
type owedMessage struct {
	key, id  string
	delivery *Delivery
}

// handOverOwed hands h what the state file records as owed, in delivery order,
// and returns h's first error beside the error that ends the loop at once.
func (b *Mailbox) handOverOwed(ctx context.Context, h Handler) (stopped, err error) {
	owed, err := lockedFinished(ctx, b, b.owedMessages)
	if err != nil {
		return nil, err
	}
	for _, m := range owed {
		var herr error
		if m.delivery == nil {
			if herr = h(ctx, Event{Kind: EventOwedGone, ID: m.id}); herr == nil {
				herr = b.clearOwed(ctx, m.key, m.id, false)
			}
		} else {
			herr = b.handOwed(ctx, m.delivery, nil, h)
		}
		if herr != nil && stopped == nil {
			stopped = herr
		}
	}
	return stopped, nil
}

// owe records in s that the message id is owed its handling, and returns the
// record's key: a number that rises with each, so the bucket's order is the
// order of delivery.
func owe(s store, id string) (string, error) {
	n, err := s.next(bucketOwed)
	if err != nil {
		return "", err
	}
	key := serialKey(n)
	return key, s.put(bucketOwed, key, []byte(id))
}

// clearOwed ends the record under key of the message id, and in the same
// transaction records that its handling gave up, where it did. While another
// command holds the lock it waits on, for as long as ctx lasts: a message whose
// record stays is handed over again.
func (b *Mailbox) clearOwed(ctx context.Context, key, id string, gaveUp bool) error {
	for {
		err := b.lockedFinishedDo(ctx, func() error {
			return b.update(func(s store) error {
				if gaveUp {
					if err := s.put(bucketGaveUp, id, nil); err != nil {
						return err
					}
				}
				return s.del(bucketOwed, key)
			})
		})
		if !errors.Is(err, errLocked) {
			return err
		}
	}
}

// owedMessages reads what the state file records as owed, under the command that
// holds the lock, and drops the record of a handling that gave up where inbox/
// no longer keeps its message. An owed message's file that others can reach,
// or that another user owns, is refused before it is read (judgeKept), and
// nothing is handed over.
func (b *Mailbox) owedMessages() ([]owedMessage, error) {
	if err := b.keysMatch(); err != nil {
		return nil, err
	}
	var owed []owedMessage
	var left []string
	if err := b.view(func(s store) error {
		if err := s.each(bucketOwed, func(k, v []byte) error {
			owed = append(owed, owedMessage{key: string(k), id: string(v)})
			return nil
		}); err != nil {
			return err
		}
		return s.each(bucketGaveUp, func(k, _ []byte) error {
			if !exists(b.path("inbox", string(k)+".json")) {
				left = append(left, string(k))
			}
			return nil
		})
	}); err != nil {
		return nil, err
	}
	for i := range owed {
		id := owed[i].id
		if !hexID.MatchString(id) {
			return nil, fmt.Errorf("the mailbox's state file records a message owed under an id that is not one")
		}
		if err := b.judgeKept(b.path("inbox", id+".json")); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(b.path("inbox", id+".json")) //nolint:gosec // a path inside the mailbox's own directory, named by 64 hex
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if line, _, env, ok := b.keptLine(data); ok {
			owed[i].delivery = &Delivery{ID: id, Envelope: env, Line: line, owed: owed[i].key}
		}
	}
	if len(left) == 0 {
		return owed, nil
	}
	return owed, b.update(func(s store) error {
		for _, id := range left {
			if err := s.del(bucketGaveUp, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// owedCounts is what Status says of the owed records: how many messages are
// owed their handling, and the ids of those whose handling gave up that
// inbox/ still keeps.
func (b *Mailbox) owedCounts() (owed int, gaveUp []string, err error) {
	err = b.view(func(s store) error {
		if err := s.each(bucketOwed, func(_, _ []byte) error { owed++; return nil }); err != nil {
			return err
		}
		return s.each(bucketGaveUp, func(k, _ []byte) error {
			if b.listedGaveUp(k) {
				gaveUp = append(gaveUp, string(k))
			}
			return nil
		})
	})
	return owed, gaveUp, err
}

// listedGaveUp reports whether Status lists the record of a handling that gave
// up under key: one named by a message's id whose file inbox/ keeps.
func (b *Mailbox) listedGaveUp(key []byte) bool {
	return hexID.Match(key) && exists(b.path("inbox", string(key)+".json"))
}

// ClearFailed deletes the record of every message whose handling gave up
// ([ErrGaveUp]), in one transaction, so that [Mailbox.Status] lists none until a
// handling gives up again. It returns how many of them Status listed, those
// inbox/ keeps, and drops the records of those inbox/ no longer keeps too. Each
// message stays in inbox/, and nothing hands one over again. It writes the
// mailbox, so it waits while another command holds the lock, and a mailbox that
// holds its own revocation runs it too: it signs and asks nothing.
func (b *Mailbox) ClearFailed(ctx context.Context) (int, error) {
	return lockedFinished(ctx, b, func() (int, error) {
		listed := 0
		err := b.update(func(s store) error {
			var keys []string
			if err := s.each(bucketGaveUp, func(k, _ []byte) error {
				if b.listedGaveUp(k) {
					listed++
				}
				keys = append(keys, string(k))
				return nil
			}); err != nil {
				return err
			}
			for _, k := range keys {
				if err := s.del(bucketGaveUp, k); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		return listed, nil
	})
}
