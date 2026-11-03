package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/spf13/cobra"
)

// recvOptions is what recv was asked to do beside a drain.
type recvOptions struct {
	wait    time.Duration
	follow  bool
	endless bool // --follow with no --wait
	headers bool
}

// fatalEnd is a recv that ended on a refusal no wait mends (client.Fatal): exit
// status 4, and "fatal" in its "error" line.
type fatalEnd struct{ err error }

func (e *fatalEnd) Error() string { return e.err.Error() }
func (e *fatalEnd) Unwrap() error { return e.err }

// recv runs b's receive-and-hold loop (client.Mailbox.Follow), printing each of
// its events: bare, one drain; with --wait, until its first delivery or its
// deadline; with --follow, past each delivery. It ends on a fatal refusal
// as fatalEnd, and interrupted under --follow with no error.
func (e *cli) recv(c *cobra.Command, b *client.Mailbox, o recvOptions) error {
	ctx := c.Context()
	var f client.FollowOptions
	if !o.endless {
		f.Until = time.Now().Add(o.wait)
	}
	r := &recvRun{cli: e, b: b, o: o, stale: true}
	err := b.Follow(ctx, f, r.handle)
	switch {
	case o.follow && ctx.Err() != nil && errors.Is(err, ctx.Err()):
		err = nil
	case err != nil && client.Fatal(err):
		return &fatalEnd{err}
	case err != nil:
		return err
	}
	if r.renewErr != nil || r.aclErr != nil || r.notMade != nil || r.replaced {
		return &needsALook{}
	}
	return nil
}

// recvRun is one recv's state, which its events' lines and its exit status
// come from.
type recvRun struct {
	*cli
	b     *client.Mailbox
	o     recvOptions
	names names
	stale bool // names are read again at the next delivery: --follow runs for weeks
	got   bool // a drain delivered a message
	// werr is the first failed write to stdout: every delivery is also kept in
	// inbox/, and every refusal in refused/, so it loses nothing, and it ends
	// the run once the drain's events are handed over.
	werr error
	// renewErr is a renewal that was due and failed, and that no later drain
	// of this run made good; aclErr the same for the mailbox's ACL; notMade what
	// the mailbox's last read of itself served that it did not make; replaced that
	// a renewal of this run found an ACL at the server that the mailbox had not
	// sent, and replaced it.
	renewErr, aclErr error
	notMade          *client.NotMade
	replaced         bool
}

func (r *recvRun) print(l []byte) {
	if err := r.out(l); err != nil && r.werr == nil {
		r.werr = err
	}
}

// handle prints one event.
func (r *recvRun) handle(ctx context.Context, ev client.Event) error {
	switch ev.Kind {
	case client.EventDelivery:
		r.got = true
		if r.stale {
			r.names, r.stale = namesOf(ctx, r.b), false
		}
		// A message line is this client's own, from the verified fields:
		// never the sender's bytes.
		l := message(*ev.Delivery, r.names)
		if r.o.headers {
			l = messageHeaders(*ev.Delivery, r.names)
		}
		r.print(l)
	case client.EventDuplicate:
		r.print(line("duplicate", "id", ev.ID, "detail", "a retry of a message already delivered: not delivered again"))
	case client.EventRefusal:
		r.print(line("refused", "id", ev.Refusal.ID, "code", ev.Refusal.Code, "detail", ev.Refusal.Reason))
	case client.EventHeld:
		r.print(line("held", "id", ev.Delivery.ID, "from", ev.Delivery.Envelope.From, "detail",
			"from a sender this mailbox has not allowed: kept in held/ (dromas held list lists it, and dromas peer allow delivers it)"))
	case client.EventBlocked:
		r.print(line("dropped", "id", ev.ID, "detail", "from a sender this mailbox blocked: dropped"))
	case client.EventMore:
		if again := seconds(ev.Result.Again()); ev.Result.Paced > 0 {
			r.print(line("paced", "retry_after", again, "detail",
				fmt.Sprintf("the server paces how many messages this mailbox takes out: the rest is still on the server; run recv again in %d seconds or later", again)))
		} else {
			r.print(line("more", "detail", "this run stopped at the most one drain takes: run recv again for the rest, which is still on the server"))
		}
	case client.EventRenewed:
		r.renewErr = nil
	case client.EventRenewFailed:
		r.renewErr = ev.Err
		r.print(line("renewal_failed", failure(fmt.Errorf("the prekey is due for renewal and was not renewed: recv tries again within the hour, and dromas status says whether it went through: %w", ev.Err))...))
	case client.EventNotMade:
		r.print(notMadeLine(ev.Result.NotMade))
	case client.EventACLFailed:
		r.print(line("acl_not_sent", failure(fmt.Errorf("the next recv sends this mailbox's ACL, and dromas status says whether it went through: %w", ev.Err))...))
	case client.EventACLReplaced:
		r.replaced = true
		r.print(line("acl_replaced", "detail", "this mailbox's server held an ACL other than the one this mailbox last sent it, which another machine of this mailbox, someone holding its pass or the server itself wrote: this mailbox sent its own in its place"))
	case client.EventDrained:
		r.notMade, r.aclErr, r.stale = ev.Result.NotMade, ev.Result.ACLErr, true
		switch {
		case r.werr != nil:
			return r.werr
		case r.got && !r.o.follow:
			return client.ErrStop
		}
	case client.EventRetry:
		r.say(line("waiting", append([]any{"for", "server"}, failure(ev.Err)...)...))
	case client.EventConnectionDropped:
		r.say(line("waiting", "for", "connection", "held", seconds(ev.Hold.After), "wait", seconds(ev.Hold.Wait), "keep_alive", seconds(ev.Hold.KeepAlive),
			"detail", "the connection dropped while the list was held: the next one keeps alive, and the wait shortens only if that one drops too"))
	}
	return nil
}
