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
	// EventDrained ends each drain's events, and carries its [Result].
	EventDrained
	// EventConnectionDropped is a connection that dropped while the list was
	// held: [Event.Hold].
	EventConnectionDropped
	// EventRetry is a failed drain or held request that the loop makes again:
	// [Event.Err].
	EventRetry
	// EventOwedGone is an owed message that inbox/ no longer keeps as recv wrote
	// it, so the handler, or the webhook's ([FollowOptions.Webhook]), is not
	// handed it: [Event.ID].
	EventOwedGone
	// EventSent is a message of the outbox that a flush posted, under
	// [FollowOptions.Flush]: [Event.Sent].
	EventSent
	// EventSendFailed is a flush that left messages in the outbox, under
	// [FollowOptions.Flush]: [Event.Err] names each.
	EventSendFailed
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
	// Err is why, in an EventRenewFailed, an EventACLFailed, an EventRetry or
	// an EventSendFailed.
	Err error
	// Sent is the message of an EventSent.
	Sent *Sent
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

// ErrGaveUp is what a [Handler] returns where [FollowOptions.Owed] is set, or
// the webhook's ([FollowOptions.Webhook]), for a delivery whose handling failed
// for good. [Mailbox.Follow] records it, for [Mailbox.Status] to list under
// attention until [Mailbox.ClearFailed] clears every such record, and goes on:
// nothing tries it again.
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
	// Flush posts the outbox's messages that are due after each drain that
	// succeeds, as [Mailbox.FlushReport] does but for the entries whose wait is not over, and ends a hold when the next waiting one
	// comes due, so a message a refusal left waiting goes once its
	// Retry-After has passed.
	Flush bool
	// Webhook, when not nil, is a second handling of each delivery, apart
	// from the handler's, with owed and gave-up records of its own: a
	// failure of either never stops, delays or fails the other. The
	// transaction that records a message landed in inbox/ as seen records it
	// as owed to Webhook too. Webhook runs on a goroutine of its own, and is
	// handed an [EventDelivery] for each owed message, in delivery order, one
	// at a time, beginning with what is owed when Follow starts, or an
	// [EventOwedGone] where inbox/ no longer keeps the message. The record is
	// cleared once Webhook returns nil or [ErrGaveUp], which it records; any
	// other error leaves the message owed, for the next handover. Follow
	// waits for Webhook to hand over what is owed before it returns, but
	// where its context ended: what is cut short stays owed for the next
	// Follow.
	Webhook Handler
}

// records are the owed and gave-up records of one handling of each delivery,
// by their buckets: the handler's, under [FollowOptions.Owed], and the
// webhook's, under [FollowOptions.Webhook].
type records struct{ owed, gaveUp string }

var (
	handlerRecords = records{bucketOwed, bucketGaveUp}
	webhookRecords = records{bucketWebhookOwed, bucketWebhookGaveUp}
)

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
// a drain refused for what a renewal mends among them where that renewal failed
// so ([Mailbox.Receive]), and the loop drains again after the error's
// Retry-After, or the later of the server's poll interval and its pace, or a
// minute. Under [FollowOptions.Owed] so is a handover of what is owed that
// such a fault cuts short, at reading the records or at clearing one: what is
// left stays owed, and is handed over again, in order, before the next drain.
// A folder of the mailbox missing, or a clock behind, ends the loop as any
// refusal no wait mends does ([Fatal]), and what is owed stays owed for the
// next loop. Where the retry lands past Until the error ends the loop, as any
// other drain's error does. Follow returns that error, the handler's, ctx's
// when ctx ended, and nil for [ErrStop] or at Until. Under
// [FollowOptions.Webhook] it returns, where it would return nil, the fault
// that cut the webhook's last handover short.
func (b *Mailbox) Follow(ctx context.Context, o FollowOptions, h Handler) (err error) {
	// The loop's goroutine and the webhook's are one command (claim).
	ctx = context.WithValue(ctx, followingKey{}, true)
	// delivered is called after each drain that delivered a message to inbox/.
	delivered := func() {}
	if o.Webhook != nil {
		w := &webhooks{b: b, h: o.Webhook, kicked: make(chan struct{}, 1), done: make(chan struct{})}
		ended := make(chan error, 1)
		go func() { ended <- w.run(ctx) }()
		delivered = w.kick
		defer func() {
			close(w.done)
			if werr := <-ended; err == nil && ctx.Err() == nil {
				err = werr
			}
		}()
	}
	if h == nil {
		h = noHandler
	}
	between := o.Between
	if between == nil {
		between = func(context.Context) bool { return false }
	}
	var stopped error
	keep := func(err error) {
		if err != nil && stopped == nil {
			stopped = err
		}
	}
	// owed is whether what the state file records as owed is handed over
	// before the next drain: at the start, and after a handover a fault cut short.
	owed := o.Owed
	var owing []records
	if o.Owed {
		owing = append(owing, handlerRecords)
	}
	if o.Webhook != nil {
		owing = append(owing, webhookRecords)
	}
	for {
		var res *Result
		var err error
		if owed {
			var serr error
			serr, err = b.handOverOwed(ctx, handlerRecords, h)
			keep(serr)
			owed = err != nil
		}
		if err == nil {
			res, err = locked(ctx, b, func() (*Result, error) { return b.receive(ctx, owing) })
			if res != nil && len(res.Delivered) > 0 {
				delivered()
			}
			serr, fault := b.handOver(ctx, o.Owed, res, h)
			keep(serr)
			if fault != nil {
				owed = true
				if err == nil {
					err = fault
				}
			}
		}
		// The outbox's due entries, after a drain that succeeded, and when the
		// next one is due.
		var due time.Time
		if o.Flush && err == nil && stopped == nil {
			var ferr error
			due, ferr = b.flushFollowing(ctx, h)
			keep(ferr)
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
			latest := time.Now().Add(drainWithin(res))
			if !due.IsZero() && due.Before(latest) {
				latest = due
			}
			if err := b.holdNext(ctx, o, between, again, pause, latest, h); err != nil {
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

// flushFollowing flushes the outbox for Follow, and hands h an EventSent for
// each message posted and an EventSendFailed for what the flush left. It
// returns when the next entry left waiting is due, zero for none, and h's
// first error.
func (b *Mailbox) flushFollowing(ctx context.Context, h Handler) (time.Time, error) {
	// Of the entries whose wait is over alone, each other one queued with
	// when it is due, and nothing asked about it.
	fl, err := lockedFinished(ctx, b, func() (*Flushed, error) {
		if err := b.keysMatch(); err != nil {
			return nil, err
		}
		return b.flush(ctx, true)
	})
	var stopped error
	keep := func(e error) {
		if e != nil && stopped == nil {
			stopped = e
		}
	}
	var due time.Time
	if fl != nil {
		for _, s := range fl.Sent {
			keep(h(ctx, Event{Kind: EventSent, Sent: s}))
		}
		for _, q := range fl.Queued {
			if due.IsZero() || q.At.Before(due) {
				due = q.At
			}
		}
	}
	if err != nil {
		keep(h(ctx, Event{Kind: EventSendFailed, Err: err}))
	}
	// Never sooner than the poll budget lets a drain follow: an entry left
	// waiting with no Retry-After is due at once, and is tried again at the
	// next drain.
	if soonest := time.Now().Add(MinHold); !due.IsZero() && due.Before(soonest) {
		due = soonest
	}
	return due, stopped
}

// ended is what Follow returns for a handler's error.
func ended(err error) error {
	if errors.Is(err, ErrStop) {
		return nil
	}
	return err
}

// pauseAfter is how long the loop waits before the drain after res and err:
// for an answer about a revocation left unproven, the failed read's wait.
func pauseAfter(res *Result, err error) time.Duration {
	if up := (*unproven)(nil); errors.As(err, &up) {
		err = up.read
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

// handOver hands h each event of one drain, res, and returns h's first error
// beside the first fault at clearing what was owed. The deliveries after that
// fault are not handed to h: they stay owed, for the loop to hand over again.
func (b *Mailbox) handOver(ctx context.Context, owed bool, res *Result, h Handler) (stopped, fault error) {
	if res == nil {
		return nil, nil
	}
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
		switch {
		case !owed:
			give(Event{Kind: EventDelivery, Delivery: d})
		case fault == nil:
			var herr error
			herr, fault = b.handOwed(ctx, handlerRecords, d, res, h)
			keep(herr)
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
	give(Event{Kind: EventDrained})
	return stopped, fault
}

// handOwed hands h a delivery that is owed its handling, and clears the record
// once the handling has ended: h returned nil, ErrStop or ErrGaveUp, which it
// records. It returns h's error, but for ErrGaveUp, which the loop goes on
// after, beside a fault at clearing the record, which leaves the delivery owed.
func (b *Mailbox) handOwed(ctx context.Context, r records, d *Delivery, res *Result, h Handler) (stopped, fault error) {
	step("handler begun")
	err := h(ctx, Event{Kind: EventDelivery, Delivery: d, Result: res})
	step("handler returned")
	gaveUp := errors.Is(err, ErrGaveUp)
	if err != nil && !gaveUp && !errors.Is(err, ErrStop) {
		return err, nil
	}
	if gaveUp {
		err = nil
	}
	if cerr := b.clearOwed(ctx, r, d.owed, d.ID, gaveUp); cerr != nil {
		return err, cerr
	}
	step("owed cleared")
	return err, nil
}

// owedMessage is a message the state file records as owed its handling: the
// delivery inbox/ keeps, or none where inbox/ no longer keeps it as recv wrote it.
type owedMessage struct {
	key, id  string
	delivery *Delivery
}

// handOverOwed hands h what the state file records as owed in r, in delivery
// order, and returns h's first error beside the fault that cut the handover
// short: one at reading the records, or at clearing one, after which the rest
// are not handed to h and stay owed (Follow).
func (b *Mailbox) handOverOwed(ctx context.Context, r records, h Handler) (stopped, fault error) {
	owed, err := lockedFinished(ctx, b, func() ([]owedMessage, error) { return b.owedMessages(r) })
	if err != nil {
		return nil, err
	}
	for _, m := range owed {
		var herr, cerr error
		if m.delivery == nil {
			if herr = h(ctx, Event{Kind: EventOwedGone, ID: m.id}); herr == nil {
				cerr = b.clearOwed(ctx, r, m.key, m.id, false)
			}
		} else {
			herr, cerr = b.handOwed(ctx, r, m.delivery, nil, h)
		}
		if herr != nil && stopped == nil {
			stopped = herr
		}
		if cerr != nil {
			return stopped, cerr
		}
	}
	return stopped, nil
}

// owe records in s that the message id is owed its handling of r, and returns
// the record's key: a number that rises with each, so the bucket's order is
// the order of delivery.
func owe(s store, r records, id string) (string, error) {
	n, err := s.next(r.owed)
	if err != nil {
		return "", err
	}
	key := serialKey(n)
	return key, s.put(r.owed, key, []byte(id))
}

// clearOwed ends the record under key of the message id, and in the same
// transaction records that its handling gave up, where it did. While another
// command holds the lock it waits on, for as long as ctx lasts: a message whose
// record stays is handed over again.
func (b *Mailbox) clearOwed(ctx context.Context, r records, key, id string, gaveUp bool) error {
	for {
		err := b.lockedFinishedDo(ctx, func() error {
			return b.update(func(s store) error {
				if gaveUp {
					if err := s.put(r.gaveUp, id, nil); err != nil {
						return err
					}
				}
				return s.del(r.owed, key)
			})
		})
		if !errors.Is(err, errLocked) {
			return err
		}
	}
}

// owedMessages reads what the state file records as owed in r, under the command that
// holds the lock, and drops the record of a handling that gave up where inbox/
// no longer keeps its message. An owed message's file that others can reach,
// or that another user owns, is refused before it is read (judgeKept), and
// nothing is handed over.
func (b *Mailbox) owedMessages(r records) ([]owedMessage, error) {
	if err := b.keysMatch(); err != nil {
		return nil, err
	}
	var owed []owedMessage
	var left []string
	if err := b.view(func(s store) error {
		if err := s.each(r.owed, func(k, v []byte) error {
			owed = append(owed, owedMessage{key: string(k), id: string(v)})
			return nil
		}); err != nil {
			return err
		}
		return s.each(r.gaveUp, func(k, _ []byte) error {
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
			if err := s.del(r.gaveUp, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// owedCounts is what Status says of the owed records of r: how many messages
// are owed their handling, and the ids of those whose handling gave up that
// inbox/ still keeps.
func (b *Mailbox) owedCounts(r records) (owed int, gaveUp []string, err error) {
	err = b.view(func(s store) error {
		if err := s.each(r.owed, func(_, _ []byte) error { owed++; return nil }); err != nil {
			return err
		}
		return s.each(r.gaveUp, func(k, _ []byte) error {
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
// ([ErrGaveUp]), the handler's and the webhook's, in one transaction, so that
// [Mailbox.Status] lists none until a handling gives up again. It returns how many of them Status listed, those
// inbox/ keeps, and drops the records of those inbox/ no longer keeps too. Each
// message stays in inbox/, and nothing hands one over again. It writes the
// mailbox, so it waits while another command holds the lock, and a mailbox that
// holds its own revocation runs it too: it signs and asks nothing.
func (b *Mailbox) ClearFailed(ctx context.Context) (int, error) {
	return lockedFinished(ctx, b, func() (int, error) {
		listed := 0
		err := b.update(func(s store) error {
			for _, r := range []records{handlerRecords, webhookRecords} {
				var keys []string
				if err := s.each(r.gaveUp, func(k, _ []byte) error {
					if b.listedGaveUp(k) {
						listed++
					}
					keys = append(keys, string(k))
					return nil
				}); err != nil {
					return err
				}
				for _, k := range keys {
					if err := s.del(r.gaveUp, k); err != nil {
						return err
					}
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

// webhooks hands each delivery owed the webhook to its handler, on a goroutine
// of its own ([FollowOptions.Webhook]).
type webhooks struct {
	b *Mailbox
	h Handler
	// kicked holds one kick, a drain's deliveries since the last handover
	// began; done is closed when Follow's loop has ended.
	kicked chan struct{}
	done   chan struct{}
}

// webhookRetry is how soon a handover that a fault cut short is tried again,
// where no delivery kicks one sooner: Follow's own wait for a drain that
// failed with no Retry-After.
const webhookRetry = time.Minute

// kick asks for a handover of what is owed, after the one under way.
func (w *webhooks) kick() {
	select {
	case w.kicked <- struct{}{}:
	default:
	}
}

// run hands over what is owed at the start, again at each kick, and once more
// after a fault (webhookRetry), until Follow's loop ends. It then hands over
// what the last kick asked for, and returns the last handover's fault.
func (w *webhooks) run(ctx context.Context) error {
	for {
		err := w.handOver(ctx)
		var retry <-chan time.Time
		if err != nil {
			retry = time.After(webhookRetry)
		}
		select {
		case <-w.kicked:
		case <-retry:
		case <-w.done:
			select {
			case <-w.kicked:
				return w.handOver(ctx)
			default:
				return err
			}
		}
	}
}

// handOver hands the webhook's handler what is owed it. Its handler's errors
// leave a message owed, and are not a fault of the handover.
func (w *webhooks) handOver(ctx context.Context) error {
	_, fault := w.b.handOverOwed(ctx, webhookRecords, w.h)
	return fault
}
