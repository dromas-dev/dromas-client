package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/spf13/cobra"
)

const (
	// execTries is how many times --exec's program runs for one message at most.
	execTries = 3
	// execPermanent is the exit status that says a run failed for good, and
	// is not tried again: qmail's.
	execPermanent = 100
)

// execDelay is the wait between two runs for one message: long enough for a
// fault of the network that failed the first to clear, short enough that
// the next message waits little behind it.
var execDelay = 30 * time.Second

// execWaitDelay is how long a process that ended, or was killed, has for
// whatever it left running to let go of its stdin (exec.Cmd.WaitDelay).
const execWaitDelay = 5 * time.Second

// recvOptions is what recv was asked to do beside a drain.
type recvOptions struct {
	wait        time.Duration
	follow      bool
	endless     bool // --follow with no --wait
	headers     bool
	exec        []string // --exec's program and arguments
	execTimeout time.Duration
}

// takesArguments allows recv arguments only after --, where they are --exec's.
func (o *recvOptions) takesArguments(c *cobra.Command, args []string) error {
	if dash := c.ArgsLenAtDash(); dash > 0 || dash < 0 && len(args) > 0 {
		return wrong(c, "%s takes arguments only after --, for --exec", c.CommandPath())
	}
	return nil
}

// check holds the flags of --exec to each other, and its program to one that
// can start, so a resident process with a wrong one refuses at its start
// rather than at each message.
func (o *recvOptions) check(c *cobra.Command, program string, args []string) error {
	path := c.CommandPath()
	switch {
	case program == "" && c.Flags().Changed("exec"):
		return wrong(c, "%s --exec takes a program", path)
	case program == "" && len(args) > 0:
		return wrong(c, "%s takes arguments only after --, for --exec", path)
	case program == "":
		return nil
	case !o.follow && !c.Flags().Changed("wait"):
		return wrong(c, "%s --exec goes with --wait or --follow", path)
	case o.execTimeout <= 0:
		return wrong(c, "%s --exec-timeout takes a duration above zero, such as 5m", path)
	}
	if _, err := exec.LookPath(program); err != nil {
		return wrong(c, "%s --exec: %v", path, err)
	}
	return nil
}

// fromConfig takes the config file's [recv] exec where the command line names
// no --exec and recv waits: the program, then its arguments. So a service
// manager's unit names only the profile. --exec-timeout goes with a program
// from either.
func (o *recvOptions) fromConfig(c *cobra.Command, cfg *config, program string, args []string) (string, []string, error) {
	if program == "" && cfg.exec != nil && (o.follow || c.Flags().Changed("wait")) {
		program, args = cfg.exec[0], cfg.exec[1:]
		if _, err := exec.LookPath(program); err != nil {
			return "", nil, &configError{file: cfg.file, key: "recv.exec", msg: fmt.Sprintf("%s: recv.exec: %v", cfg.file, err)}
		}
	}
	if program == "" && c.Flags().Changed("exec-timeout") {
		return "", nil, wrong(c, "%s --exec-timeout goes with --exec, or with recv.exec in the config file", c.CommandPath())
	}
	return program, args, nil
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
	f := client.FollowOptions{Owed: o.exec != nil, Flush: o.follow}
	if !o.endless {
		f.Until = time.Now().Add(o.wait)
	}
	r := &recvRun{cli: e, b: b, o: o, stale: true}
	err := b.Follow(ctx, f, r.handle)
	if o.follow && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		err = nil
	}
	switch {
	case client.Fatal(err):
		return &fatalEnd{err}
	case err != nil:
		return err
	}
	if r.renewErr != nil || r.aclErr != nil || r.notMade != nil || r.replaced || r.gaveUp {
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
	// sent, and replaced it; and gaveUp that a callback ran out of tries.
	renewErr, aclErr error
	notMade          *client.NotMade
	replaced, gaveUp bool
}

func (r *recvRun) print(l []byte) {
	if err := r.out(l); err != nil && r.werr == nil {
		r.werr = err
	}
}

// handle prints one event, and runs --exec's program for a delivery.
func (r *recvRun) handle(ctx context.Context, ev client.Event) error {
	switch ev.Kind {
	case client.EventDelivery:
		r.got = true
		if r.stale {
			r.names, r.stale = namesOf(ctx, r.b), false
		}
		// A message line is this client's own, from the verified fields:
		// never the sender's bytes.
		// --headers changes what is printed, never what the callback gets:
		// so a service whose stdout is a log logs who wrote and when, and
		// its callback still reads the message.
		l := message(*ev.Delivery, r.names)
		printed := l
		if r.o.headers {
			printed = messageHeaders(*ev.Delivery, r.names)
		}
		r.print(printed)
		if r.o.exec != nil {
			return r.callback(ctx, ev.Delivery.ID, l)
		}
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
			r.print(line("rate_limited", "retry_after", again, "detail",
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
		r.print(line("acl_sync_failed", failure(fmt.Errorf("the next recv sends this mailbox's ACL, and dromas status says whether it went through: %w", ev.Err))...))
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
		r.say(line("waiting", append([]any{"reason", "server"}, failure(ev.Err)...)...))
	case client.EventConnectionDropped:
		r.say(line("waiting", "reason", "connection", "held_seconds", seconds(ev.Hold.After), "wait_seconds", seconds(ev.Hold.Wait), "keep_alive_seconds", seconds(ev.Hold.KeepAlive),
			"detail", "the connection dropped while the list was held: the next one keeps alive, and the wait shortens only if that one drops too"))
	case client.EventSent:
		r.print(sent(ev.Sent, r.names))
	case client.EventSendFailed:
		r.print(line("send_failed", failure(fmt.Errorf("messages wait in the outbox: recv --follow posts each again once it is due, and dromas outbox flush does it now: %w", ev.Err))...))
	case client.EventOwedGone:
		r.print(line("exec_skipped", "id", ev.ID, "detail", "this message was owed its callback, and inbox/ no longer keeps it as recv wrote it: the callback did not run"))
	}
	return nil
}

// callback runs --exec's program for one message, with line on stdin, up to
// execTries times, and says each failed try. It returns client.ErrGaveUp where
// no try is left, and ctx's error where ctx ended a run, whose message stays
// owed for the next start.
func (r *recvRun) callback(ctx context.Context, id string, l []byte) error {
	stdin := append(bytes.Clone(l), '\n')
	for try := 1; ; try++ {
		permanent, how := r.runOnce(ctx, stdin)
		if err := ctx.Err(); err != nil {
			return err
		}
		if how == nil {
			return nil
		}
		r.print(line("exec_attempt_failed", append(append([]any{"id", id, "attempt", try}, how...),
			"detail", fmt.Sprintf("the callback failed at try %d of at most %d", try, execTries))...))
		if permanent || try == execTries {
			r.gaveUp = true
			r.print(line("exec_failed", "id", id, "attempts", try, "detail",
				"the callback has no try left: inbox/ keeps the message, nothing tries it again, and dromas status lists it until dromas inbox clear-failed"))
			return client.ErrGaveUp
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(execDelay):
		}
	}
}

// runOnce runs --exec's program once, and says how it failed, as the members of
// an "exec_attempt_failed" line, or nil where it exited 0. Its stdout and stderr go
// nowhere, so nothing it writes is read, and a sender's words it passes on reach
// no line.
func (r *recvRun) runOnce(ctx context.Context, stdin []byte) (permanent bool, how []any) {
	run, cancel := context.WithTimeout(ctx, r.o.execTimeout)
	defer cancel()
	cmd := exec.CommandContext(run, r.o.exec[0], r.o.exec[1:]...) //nolint:gosec // the program the mailbox's owner named on the command line, run with no shell but cmd.exe for a .bat or .cmd on Windows (os/exec's Command)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.WaitDelay = execWaitDelay
	err := cmd.Run()
	ps := cmd.ProcessState
	timedOut := ctx.Err() == nil && run.Err() != nil
	switch {
	case ps != nil && ps.Success():
		return false, nil
	case ps == nil:
		return false, []any{"start_error", err.Error()}
	}
	if timedOut {
		how = append(how, "timed_out", true)
	}
	if st, ok := ps.Sys().(syscall.WaitStatus); ok && st.Signaled() {
		return false, append(how, "signal", st.Signal().String())
	}
	return ps.ExitCode() == execPermanent, append(how, "exit_status", ps.ExitCode())
}
