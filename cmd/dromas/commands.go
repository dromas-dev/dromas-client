package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
	"github.com/spf13/cobra"
)

// tree is every command: the everyday ones at the top level, and a group for
// each thing that has more than one thing done to it.
func (e *cli) tree() *cobra.Command {
	root := e.group("dromas", "", "",
		e.initCmd(), e.addrCmd(), e.sendCmd(), e.recvCmd(), e.statusCmd(), e.configCmd(), e.versionCmd(),
		e.group("inbox", "messages recv delivered: list, show, clear-failed",
			`recv keeps each message it delivers in inbox/, as the "message" line it printed,
until you delete the file: deleting the file forgets the message.

These commands print those lines again, or clear what dromas status reports
about --exec programs that gave up. They call no server.`,
			e.inboxListCmd(), e.inboxShowCmd(), e.inboxClearFailedCmd()),
		e.group("held", "messages from unknown senders, held to allow or block: list",
			`A message that passed every check, from an unknown sender (one this mailbox has
not written to, named or allowed), is kept in held/, and recv does not print it.
dromas peer allow delivers a sender's held messages to inbox/, and dromas peer
block deletes them.

A message that failed a check goes to refused/, never to held/.`,
			e.heldListCmd()),
		e.group("peer", "who can write to this mailbox: allow, block, list",
			`A message from a sender this mailbox has not allowed is held in held/ (dromas
held list lists it), and a message from a sender it blocked is dropped. Sending
to a mailbox, or naming it with dromas alias add, also allows it.

The config file's [policy] table sets where a message from a sender you have not
allowed goes, held/ or inbox/, and whose messages the server accepts: dromas
help describes it.`,
			e.allowCmd(), e.blockCmd(), e.peerListCmd()),
		e.group("alias", "names for peers, usable in place of a link: add, rm, list",
			`An alias is this mailbox's own name for a peer. The mailbox keeps its aliases in
its aliases file as "<name> <address>" lines, which you can also write by hand.

A name allows its peer, as dromas peer allow does. A name never moves to another
address: to change one, remove it and add it again.`,
			e.aliasAddCmd(), e.aliasRmCmd(), e.aliasListCmd()),
		e.group("outbox", "messages the server has not accepted yet: flush",
			`A message stays in outbox/ until a server accepts it. A message that can never
be delivered moves to outbox/failed/: delete it there to drop it.`,
			e.flushCmd()),
		e.group("refused", "messages that failed a check: list, rm",
			`A message that failed a check is never delivered: recv acknowledges it and keeps
it in refused/, as evidence of who sent what.

Read refused/ through these commands, which print only this client's own
wording. The files keep what the server served, with each of the server's
strings in base64 unless the string has its expected form.`,
			e.refusedListCmd(), e.refusedRmCmd()),
		e.group("prekey", "the key that senders encrypt to: renew, lifetime",
			fmt.Sprintf(`A prekey is valid for %d days, or for the shorter lifetime this mailbox is set
to. The identity signs it, or a working key under a certificate made with it.

dromas recv renews the prekey once it is a day old, so run recv at least every
%s and the mailbox never lapses. dromas prekey lifetime --help says how often
to run recv for a shorter lifetime.`, protocol.PrekeyLifetime/(24*time.Hour), formatTime(client.RecvWithin(protocol.PrekeyLifetime))),
			e.renewCmd(), e.lifetimeCmd()),
		e.group("cert", "this mailbox's certificates: switch, revoke-earlier, publish-revoked",
			`A mailbox's identity signs what the mailbox says, unless the mailbox was created
with dromas init --offline or switched with dromas cert switch. Then the
identity certifies a working key with each prekey, and the working key signs.

If a mailbox's working key may have been stolen, revoke every certificate before
a new one with dromas cert revoke-earlier: the address continues. A copy of the
whole mailbox directory holds the identity too, and then the remedy is to revoke
the address.`,
			e.switchCmd(), e.revokeEarlierCmd(), e.publishRevokedCmd()),
		e.group("account", "the account that can lift this mailbox's limits: join, leave",
			`An account can lift the limits of the mailboxes in it. The account's holder
gives you an adopt key, which puts a mailbox in the account.`,
			e.adoptCmd(), e.leaveCmd()),
		e.group("revocation", "permanently ending an address: make, publish, keep, ask",
			`A revocation permanently ends an address, and only the identity's own signature
can make one. A mailbox keeps every revocation that verifies, of an address and
of an address's certificates, and checks what it sends and receives against
them.`,
			e.makeCmd(), e.publishCmd(), e.keepCmd(), e.askCmd()),
		e.agentGroup(),
	)
	// dromas --version is dromas version, as gh's and git's are. The flag is
	// declared here so cobra adds none of its own, which would take -v.
	var showVersion bool
	root.Flags().BoolVar(&showVersion, "version", false, "print this client's version, as dromas version does")
	root.RunE = func(c *cobra.Command, args []string) error {
		if showVersion && len(args) == 0 {
			return e.version()
		}
		return e.groupRun(c, args)
	}
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpFunc(func(c *cobra.Command, _ []string) {
		_, _ = io.WriteString(e.stdout, helpText(c))
	})
	root.SetHelpCommand(command("help [<command>]", "print this help, or a command's",
		`dromas help <command> and dromas <command> --help print a command's
help, and dromas <group> help a group's.`,
		cobra.ArbitraryArgs, func(_ *cobra.Command, args []string) error {
			t, rest, err := root.Find(args)
			switch {
			case err != nil:
				return err
			case len(rest) > 0:
				return unknown(t, rest)
			}
			_, err = io.WriteString(e.stdout, helpText(t))
			return err
		}))
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return wrong(c, "%v", err) })
	return root
}

// versionCmd prints what this binary is, from the build information Go
// records in it (runtime/debug), the same that go version -m reads. It reads
// no mailbox and no environment.
func (e *cli) versionCmd() *cobra.Command {
	return command("version", "print this client's version, commit, Go release and platform",
		`Print one line: the version this client was built at (a release's tag, Go's
pseudo-version for a build between releases, or the release a build from its
source archive was given at link time), the commit when the build recorded it,
the Go release that built it, and the system and architecture it was built for.
version reads no mailbox.`,
		nargs(0, 0), func(*cobra.Command, []string) error { return e.version() })
}

// version prints the line versionCmd describes, for dromas version and
// dromas --version alike.
func (e *cli) version() error {
	version, commit := "", ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		version = bi.Main.Version
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" {
				commit = s.Value
			}
		}
	}
	if client.LinkedVersion != "" {
		version = client.LinkedVersion
	}
	return e.out(line("version", "version", orNil(version), "commit", orNil(commit),
		"go", runtime.Version(), "platform", runtime.GOOS+"/"+runtime.GOARCH))
}

// orNil is s, or nil for "", so a member nobody knows is left out.
func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (e *cli) initCmd() *cobra.Command {
	var idType, revocationFile string
	var offline, restore bool
	c := command("init [flags]", "create this mailbox and register it, and print its link",
		`Create this mailbox and register it, and print its address and link,
<server>/to/<address>, as addr does. The mailbox's identity signs what the
mailbox says.

With --offline, the identity certifies a working key with each prekey, for as
long as the server accepts one, and the working key signs. This is permanent,
because the server and the mailbox's correspondents then accept no statement
that the identity signs.

Run with an identity file and nothing else of the mailbox, init restores that
mailbox, under a certificate when the server serves a statement under one or
--offline asks for one. A sender that saw this address under a certificate
refuses a statement its identity signs: when the server has forgotten a mailbox
that was under certificates, run dromas cert switch <address> next.

A profile without a config file gets one, with each path in full.

With --revocation-file, init also writes the mailbox's revocation record, as
dromas revocation make prints it, to a new file outside every directory of the
mailbox, for the day the mailbox is lost: keep the file where only you can read
it. On a mailbox already registered, init with --revocation-file writes the
record and changes nothing else.

A mailbox whose state does not match its prekeys/, as after state is lost, or
restored from an older copy while prekeys/ was kept, has failed: it receives,
sends, delivers and renews nothing, init and peer allow included, until init
--restore acknowledges the mismatch. init --restore adopts the prekeys the state
does not name, and renews. The lost state took its replay set with it, so the
mailbox may deliver again a message it accepted before. On a mailbox whose state
matches, init --restore changes nothing, so it can run at every start. A mailbox
restored from its identity alone needs no --restore.`,
		checked(nargs(0, 0), func(c *cobra.Command, _ []string) error {
			if idType != "" && !slices.Contains(protocol.IdentityTypes(), idType) {
				return wrong(c, "%s makes an identity of the key type %s, and %q is not one", c.CommandPath(), strings.Join(protocol.IdentityTypes(), " or "), idType)
			}
			return nil
		}), func(c *cobra.Command, _ []string) error {
			opt := e.opt
			opt.WorkingKey, opt.Restore = offline, restore
			if revocationFile == "" {
				revocationFile = os.Getenv("DROMAS_REVOCATION_FILE")
			}
			if revocationFile != "" {
				p, err := expand(revocationFile, ".")
				if err == nil {
					p, err = filepath.Abs(p)
				}
				if err != nil {
					return &configError{key: "revocation-file", msg: fmt.Sprintf("revocation-file: %v", err)}
				}
				opt.RevocationFile = p
			}
			var warning any
			// A type of PROTOCOL.md §4.1 reaches every mailbox; an optional one only
			// those whose clients verify it (§7).
			if opt.IdentityType = idType; idType != "" && !protocol.AcceptsSender(nil, idType) {
				warning = fmt.Sprintf("WARNING: a mailbox whose identity is of the optional key type %s can neither write to nor hear from a mailbox whose client does not verify that type. Every client verifies ssh-ed25519, which init makes without --identity-type.", idType)
			}
			b, err := client.Init(c.Context(), e.dir, opt)
			if err != nil {
				// An acknowledgement that committed before init failed is said
				// first: the next init --restore finds nothing to acknowledge.
				if r := client.RestoredBy(err); r != nil {
					_ = e.out(line("restore", "acknowledged", r.Acknowledged(), "detail", restoreWords(*r))) // the error is init's
				}
				return err
			}
			if r := b.Restored(); r != nil {
				if err := e.out(line("restore", "acknowledged", r.Acknowledged(), "detail", restoreWords(*r))); err != nil {
					return err
				}
			}
			if b.RestoredWithoutLists() {
				words := "WARNING: this mailbox was restored without its allowed and blocked files, so the first renewal recv makes replaces the ACL its server holds with the open one they make, and a sender it blocked reaches held/ again. Put them back from the backup before the next recv."
				if w, ok := warning.(string); ok {
					words = w + " " + words
				}
				warning = words
			}
			if inGit := b.KeysInGit(); len(inGit) > 0 {
				words := strings.Join(inGit, "; ")
				if w, ok := warning.(string); ok {
					words = w + " " + words
				}
				warning = words
			}
			return e.out(line("mailbox", "address", b.Address(), "link", b.Link(), "detail", warning))
		})
	c.Flags().BoolVar(&restore, "restore", false, "acknowledge a state that does not match prekeys/, as after state is lost or restored from an older copy, so the mailbox runs again; on a state that matches, it changes nothing")
	c.Flags().BoolVar(&offline, "offline", false, "register the mailbox under a certificate, whose working key signs in place of its identity: permanent")
	c.Flags().StringVar(&revocationFile, "revocation-file", "", "write this mailbox's revocation record, as dromas revocation make prints it, to this new `file`, outside every directory of the mailbox: $DROMAS_REVOCATION_FILE")
	c.Flags().StringVar(&idType, "identity-type", "", "make the identity a key of this `type`, not ssh-ed25519: "+protocol.IdentityP256+", which every client verifies too")
	return c
}

// restoreWords says what init --restore found and did.
func restoreWords(r client.Restored) string {
	if !r.Acknowledged() {
		return "this mailbox's state matches its prekeys: nothing to acknowledge, and nothing changed"
	}
	return fmt.Sprintf("acknowledged the restore: adopted %d prekeys the state did not name, and init renews now. The lost state took its replay set with it, so a message this mailbox accepted before may be delivered again", r.Adopted)
}

func (e *cli) addrCmd() *cobra.Command {
	return command("addr", "print this mailbox's link, to give to whoever will write to it",
		`Print this mailbox's address and link, <server>/to/<address>. Give the link to
whoever will write to this mailbox.`,
		nargs(0, 0), func(*cobra.Command, []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			return e.out(line("mailbox", "address", b.Address(), "link", b.Link()))
		})
}

func (e *cli) sendCmd() *cobra.Command {
	var f client.Fields
	var asJSON bool
	c := command("send [flags] <link, address or alias> [file]", "send a message, with the body from a file or stdin",
		`Send one message to a link, a bare address or an alias, with the body from the
file, or from stdin, and print the server's id for it and its msg_id. The
message waits in outbox/ until a server accepts it (dromas outbox flush posts it
again), and is then kept in sent/ with the server's receipt.

Sending to a mailbox allows it, so a message that talks you into a send also
lets that address's messages past held/: decide a send as you would decide
dromas peer allow.

The body is text, unless --json says it is one JSON value: then an object
arrives as that object, not as a string. send refuses a JSON value its reader
would refuse, an object with a name twice among them, and sends nothing.`,
		nargs(1, 2), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			to, err := b.Destination(args[0])
			if err != nil {
				return err
			}
			var body []byte
			if len(args) == 2 {
				body, err = os.ReadFile(args[1]) //nolint:gosec // the file the user named
			} else {
				body, err = io.ReadAll(e.stdin)
			}
			if err != nil {
				return err
			}
			var s *client.Sent
			if asJSON {
				s, err = b.SendJSON(c.Context(), to, body, f)
			} else {
				s, err = b.SendWith(c.Context(), to, string(body), f)
			}
			if err != nil {
				return err
			}
			if err := e.out(sent(s, namesOf(c.Context(), b))); err != nil {
				return err
			}
			return e.aclNotSent(s.ACLErr)
		})
	// curl's --json posts what it is given with no check that it is JSON; this
	// one holds the body to its reader's rules first, so a name given twice is
	// refused, never kept once.
	c.Flags().BoolVar(&asJSON, "json", false, "the body is one JSON value, sent as that value and not as text")
	c.Flags().StringVar(&f.Subject, "subject", "", "the message's subject: any `text`")
	c.Flags().StringVar(&f.InReplyTo, "in-reply-to", "", "the `msg_id` of the message this answers, as recv printed it")
	c.Flags().StringVar(&f.Supersedes, "supersedes", "", "the `msg_id` of an earlier message of yours that this replaces, as sent/ names it")
	return c
}

func (e *cli) recvCmd() *cobra.Command {
	var o recvOptions
	var drain time.Duration
	var program, logFile string
	c := command("recv [flags] [-- <argument>...]", "receive, verify and acknowledge every message, one JSON line each",
		fmt.Sprintf(`Receive, verify and acknowledge every message. recv prints a "message" line for
each message, built from the verified fields, with this mailbox's name for the
sender as "alias" when you named one (dromas alias add). It prints a "refused"
line for each message it refused, keeping the message in refused/, and a "held"
line for each message it held, keeping the message in held/. recv also renews
the prekey once a day, so run it at least every %s and the mailbox never
lapses.

With --headers, a "message_headers" line takes the place of each "message" line.
Its "envelope" has the fields this client checked, with none of the text the
sender chose (no body and no subject), and the line has the body's size. The
--exec program still gets each "message" line. Each message is kept in inbox/ as
always, and dromas inbox show <id> prints its "message" line, so a reader can
decide by sender before reading what the sender wrote.

One drain runs for at most --drain-time, holding the mailbox's lock. At that
limit, recv stops with a "more" line, as at its other limits, and keeps what it
read of the message it was fetching, which the next recv continues from.

With --wait, recv holds the mailbox's list open between drains, and ends at its
first delivery or at the end of the duration. With --follow, recv continues
after each delivery, without end, or until the end of the --wait duration when
both are given: run it in the foreground under a service manager. An interrupt
or a SIGTERM ends --follow with exit status 0. The mailbox's lock stays free
between drains, so the mailbox's other commands can run meanwhile. With --wait
or --follow, recv makes a call again when a wait may fix it, and reports each
wait on stderr ("waiting").

With --log-file, recv appends each line it would print, on stdout and on stderr,
to the file instead, the "error" line it ends with included. A service manager
that keeps no log of its own, as Windows' Task Scheduler, otherwise keeps none
of them. recv makes the file readable by its owner alone when the file is not
there, and leaves a file that is there as it is. Nothing rotates the file.

Exit status 4, with "fatal" in the "error" line, means that no wait fixes the
refusal: a revocation, no mailbox at this address with its registration refused,
a mailbox in another format, a missing folder of the mailbox, as on an unmounted
volume, this machine's clock behind, or any other refusal by the server or by
this client. A service manager leaves such a service failed until its owner
fixes the cause and starts it again. Exit status 1 is any other failure.

With --exec, recv runs a program for each message delivered to inbox/, with the
message's "message" line on stdin, as recv prints it without --headers. --exec
needs --wait or --follow. --exec names the program, and the arguments after --
go to it one for one: no shell runs, and nothing is split or expanded. The
config file's [recv] exec names the program and its arguments as one list, for
example exec = ["/usr/local/bin/notify", "--quiet"]. recv runs that program only
with --wait or --follow, and only when the command line has no --exec, which
replaces it. The file's [recv] exec-timeout sets the default of --exec-timeout,
and the file can set it only beside exec. On Windows, cmd.exe runs a .bat or
.cmd program, and cmd.exe splits and expands its arguments.

Nothing of a message is ever on the command line. recv discards the program's
stdout and stderr. The programs run one at a time, in delivery order, with the
mailbox's lock free, so a program can run dromas. Each message is kept in inbox/
first, so the program is a notification and never the only copy. A message held
in held/ runs no program, and neither does a message that another recv receives,
so run one recv for each mailbox.

A program's exit status 0 means done, and 100 means failed for good. recv tries
again after any other exit status, a signal, a run longer than --exec-timeout
(which kills the process), or a program that does not start, up to %d tries in
all, %s apart. Each failed attempt prints an "exec_attempt_failed" line, and a
message with no attempts left an "exec_failed" line, which dromas status then
lists under attention until dromas inbox clear-failed clears it. recv then
continues with the next message, and exits with status 3 at its end.

Nothing runs that message's program again: dromas inbox show <id> | <program>
hands it to a program by hand. A process killed before a program's tries end
runs that program again when recv --exec next starts, so the program runs at
least once for each message and may run twice: it should use the "id" as the
message's name.`, formatTime(client.RecvWithin(protocol.PrekeyLifetime)), execTries, execDelay),
		checked(o.takesArguments, func(c *cobra.Command, args []string) error {
			if drain <= 0 {
				return wrong(c, "%s --drain-time takes a duration above zero, such as 15m", c.CommandPath())
			}
			return o.check(c, program, args)
		}), func(c *cobra.Command, args []string) error {
			e.opt.DrainTime = drain
			run, args, err := o.fromConfig(c, e.cfg, program, args)
			if err != nil {
				return err
			}
			if logFile != "" {
				if err := e.logTo(logFile); err != nil {
					return err
				}
			}
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if run != "" {
				o.exec = append([]string{run}, args...)
			}
			if o.follow && !c.Flags().Changed("wait") {
				o.endless = true
			}
			return e.recv(c, b, o)
		})
	c.Flags().DurationVar(&o.wait, "wait", 0, "hold the list open until a message arrives, for at most this long, however many held requests that needs")
	c.Flags().BoolVar(&o.follow, "follow", false, "continue after each message, without end or for --wait's duration")
	c.Flags().StringVar(&program, "exec", "", "run this `program` for each message, with its line on stdin, and the arguments after -- as its own")
	c.Flags().DurationVar(&o.execTimeout, "exec-timeout", 5*time.Minute, "kill --exec's program when one run takes this long")
	c.Flags().DurationVar(&drain, "drain-time", client.DefaultDrainTime, "how long one drain runs before it stops for the next recv")
	// As rclone's --log-file and Syncthing's --log-file, which append to the file
	// and rotate nothing unless asked.
	c.Flags().StringVar(&logFile, "log-file", "", "append every line recv prints, stdout's and stderr's, to this `file`")
	// As an IMAP client fetches a message's ENVELOPE apart from its body (RFC
	// 9051 §6.4.5), as email clients list headers before they open a message.
	c.Flags().BoolVar(&o.headers, "headers", false, "print each message's checked fields and its body's size, without its body or subject")
	return c
}

func (e *cli) inboxListCmd() *cobra.Command {
	var headers bool
	c := command("list [flags]", "list the messages in inbox/, one JSON line each",
		`Print a "message" line for each message in inbox/, oldest first by sent_at, as
recv printed it: the messages recv delivered that nobody has deleted since. A
file there that is not the line recv keeps is left out, and dromas inbox show
names it.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			ds, err := b.Inbox(c.Context())
			if err != nil {
				return err
			}
			return e.messageLines(c.Context(), b, ds, headers)
		})
	c.Flags().BoolVar(&headers, "headers", false, "print each message's checked fields and its body's size, without its body or subject, as recv --headers does")
	return c
}

func (e *cli) inboxShowCmd() *cobra.Command {
	var headers bool
	c := command("show <id> [flags]", "print a message in inbox/, as recv printed it",
		`Print the "message" line of a message in inbox/, by the id recv printed, as recv
prints it without --headers. An id that is not 64 lowercase hex characters is
refused before any file is read.`,
		checked(nargs(1, 1), func(c *cobra.Command, args []string) error {
			if !client.IsID(args[0]) {
				return wrong(c, "%s takes a message's id, 64 lowercase hex, as recv prints it", c.CommandPath())
			}
			return nil
		}), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			d, err := b.InboxMessage(c.Context(), args[0])
			if err != nil {
				return err
			}
			return e.messageLines(c.Context(), b, []client.Delivery{d}, headers)
		})
	c.Flags().BoolVar(&headers, "headers", false, "print the message's checked fields and its body's size, without its body or subject, as recv --headers does")
	return c
}

// inboxClearFailedCmd clears every record of a callback that gave up, and takes
// no id. A failed unit stays failed, and systemctl is-system-running says
// "degraded", until systemctl reset-failed, which with no unit resets every one
// (systemctl(1)): a callback that gave up keeps drawing attention the same way.
func (e *cli) inboxClearFailedCmd() *cobra.Command {
	return command("clear-failed", "clear what status reports of every --exec program that gave up",
		`Clear the record of every message in inbox/ whose recv --exec program gave up,
and print a "failed_cleared" line with their "count". dromas status then lists
none of them, and needs no attention for them, until a program gives up again.

clear-failed takes no id. It clears every message whose program gave up, and
none whose program still has tries left; with none, the count is 0. Each message
stays in inbox/, and nothing runs its program again: dromas inbox show <id> |
<program> hands a message to a program by hand.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			n, err := b.ClearFailed(c.Context())
			if err != nil {
				return err
			}
			return e.out(line("failed_cleared", "count", n))
		})
}

// messageLines prints a "message" line for each of ds, or with headers a
// "message_headers" line, naming each sender as b names it.
func (e *cli) messageLines(ctx context.Context, b *client.Mailbox, ds []client.Delivery, headers bool) error {
	n := namesOf(ctx, b)
	for _, d := range ds {
		l := message(d, n)
		if headers {
			l = messageHeaders(d, n)
		}
		if err := e.out(l); err != nil {
			return err
		}
	}
	return nil
}

func (e *cli) refusedListCmd() *cobra.Command {
	return command("list", "list the messages in refused/, one JSON line each",
		`List the messages in refused/: a "refused" line for each message, with its id,
the code it was refused for (docs/output.md lists each code), and the time the
server gave, only when that time has its expected form.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			qs, err := b.RefusedMessages(c.Context())
			for _, q := range qs {
				l, jerr := json.Marshal(q)
				if jerr == nil {
					jerr = e.out(typed("refused", l))
				}
				if jerr != nil && err == nil {
					err = jerr
				}
			}
			return err
		})
}

func (e *cli) refusedRmCmd() *cobra.Command {
	var all bool
	c := command("rm [flags] [<id>...]", "delete refused messages from refused/",
		`Delete each message named by the id that dromas refused list prints, or with
--all every message, and print a "removed" line for each. Nothing is deleted
when an id is not a valid id, or names no message in refused/.`,
		func(c *cobra.Command, args []string) error {
			switch {
			case all && len(args) > 0:
				return wrong(c, "%s takes ids or --all, not both", c.CommandPath())
			case !all && len(args) == 0:
				return wrong(c, "%s takes the ids to delete, or --all", c.CommandPath())
			}
			for _, id := range args {
				if !client.IsID(id) {
					return wrong(c, "%s takes messages' ids, 64 lowercase hex, as dromas refused list prints them", c.CommandPath())
				}
			}
			return nil
		}, func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			gone, err := b.RemoveRefused(c.Context(), args)
			for _, id := range gone {
				if werr := e.out(line("removed", "id", id)); werr != nil && err == nil {
					err = werr
				}
			}
			return err
		})
	c.Flags().BoolVar(&all, "all", false, "delete every message in refused/")
	return c
}

func (e *cli) statusCmd() *cobra.Command {
	return command("status", "report what this mailbox holds and what needs attention",
		`Report what this mailbox holds and what needs attention, as one "status" line,
from the mailbox's own files and with no call to the server: its format and
where it keeps each of its files, its prekey, a renewal that has not gone
through, refused messages in refused/, held messages, and messages in outbox/
that the server has not accepted yet.

status refuses a mailbox in a format other than this client's, its "error" line
naming both formats.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			s, err := b.Status(c.Context())
			if err != nil {
				return err
			}
			st, err := json.Marshal(s)
			if err != nil {
				return err
			}
			if err := e.out(typed("status", st)); err != nil {
				return err
			}
			if len(s.Attention) > 0 {
				return &needsALook{}
			}
			return nil
		})
}

func (e *cli) heldListCmd() *cobra.Command {
	return command("list", "list the messages in held/, one JSON line per sender",
		`List the messages in held/, from senders this mailbox has not allowed: an
"unknown_sender" line for each sender, with its address, the message ids, and
the first and last sent_at. held/<id>.json is the message that recv's "message"
line would have carried.

dromas peer allow delivers a sender's held messages to inbox/, and dromas peer
block deletes them.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			hs, err := b.HeldMessages(c.Context())
			for _, h := range hs {
				l, jerr := json.Marshal(h)
				if jerr == nil {
					jerr = e.out(typed("unknown_sender", l))
				}
				if jerr != nil && err == nil {
					err = jerr
				}
			}
			return err
		})
}

func (e *cli) allowCmd() *cobra.Command {
	var rebuild bool
	c := command("allow [flags] <link, address or alias>", "deliver a sender's messages to inbox/, with what held/ keeps from it",
		`Deliver a sender's messages to inbox/ from now on. allow moves what held/ keeps
from that sender to inbox/, printing each message as recv does, then prints an
"allowed" line.

--rebuild, with no sender, adds back every address an alias names or sent/
shows, for an allowed file that was lost.`,
		func(c *cobra.Command, args []string) error {
			if rebuild && len(args) > 0 {
				return wrong(c, "dromas peer allow --rebuild takes no sender: it adds back every address an alias names or sent/ shows")
			}
			if rebuild {
				return nil
			}
			return nargs(1, 1)(c, args)
		}, func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if rebuild {
				n, err := b.RebuildAllowed(c.Context())
				if err != nil {
					return err
				}
				if err := e.out(line("allowed_rebuilt", "added", n)); err != nil {
					return err
				}
				return b.SyncACL(c.Context())
			}
			a, err := b.Destination(args[0])
			if err != nil {
				return err
			}
			moved, err := b.Allow(c.Context(), a)
			n := namesOf(c.Context(), b)
			for _, d := range moved {
				if werr := e.out(message(d, n)); werr != nil && err == nil {
					err = werr
				}
			}
			if err != nil {
				return err
			}
			return e.out(line("allowed", "address", a))
		})
	c.Flags().BoolVar(&rebuild, "rebuild", false, "add back every address an alias names or sent/ shows, for an allowed file that was lost")
	return c
}

func (e *cli) blockCmd() *cobra.Command {
	return command("block <link, address or alias>", "drop a sender's messages, here and at the server",
		`Drop a sender's messages, here and at the server, and delete what held/ keeps
from that sender, then print a "blocked" line. Blocking overrides allowing.

The server then refuses that sender's next send as sender_not_allowed, which is
final, so the sender learns that it is refused.`,
		nargs(1, 1), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			a, err := b.Destination(args[0])
			if err != nil {
				return err
			}
			n, err := b.Block(c.Context(), a)
			if err != nil {
				if n > 0 {
					return fmt.Errorf("%w (%d messages held from it were deleted)", err, n)
				}
				return err
			}
			return e.out(line("blocked", "address", a, "deleted", n))
		})
}

func (e *cli) peerListCmd() *cobra.Command {
	var allowed, blocked bool
	c := command("list [flags]", "list the allowed and blocked senders",
		`List the senders this mailbox allows and blocks, one "allowed" or "blocked" line
each, with the address. The allowed senders are those the allowed file lists and
those an alias names. Blocking overrides allowing, so an address can be on both
lists.`,
		nargs(0, 0), func(cc *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if !allowed && !blocked {
				allowed, blocked = true, true
			}
			n := namesOf(cc.Context(), b)
			for _, l := range []struct {
				on   bool
				name string
				list func(context.Context) ([]protocol.Address, error)
			}{{allowed, "allowed", b.Allowed}, {blocked, "blocked", b.Blocked}} {
				if !l.on {
					continue
				}
				as, err := l.list(cc.Context())
				if err != nil {
					return err
				}
				for _, a := range as {
					if err := e.out(line(l.name, "address", a, "alias", n.of(a))); err != nil {
						return err
					}
				}
			}
			return nil
		})
	c.Flags().BoolVar(&allowed, "allowed", false, "list the senders allowed only")
	c.Flags().BoolVar(&blocked, "blocked", false, "list the senders blocked only")
	return c
}

func (e *cli) aliasAddCmd() *cobra.Command {
	return command("add <name> <link or address>", "name a peer, so that send accepts the name",
		`Name a peer for this mailbox only, so that send accepts the name. A name is up
to 32 lowercase letters, digits, '.', '_' and '-', starting with a letter or a
digit. Naming a peer allows it.`,
		nargs(2, 2), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			// The name first, so swapped arguments are told what an alias is.
			if err := client.ValidAlias(args[0]); err != nil {
				return err
			}
			to, err := b.Destination(args[1])
			if err != nil {
				return err
			}
			if err := b.SetAlias(c.Context(), args[0], to); err != nil {
				return err
			}
			if err := e.out(line("alias", "name", args[0], "address", to)); err != nil {
				return err
			}
			return b.SyncACL(c.Context())
		})
}

func (e *cli) aliasRmCmd() *cobra.Command {
	return command("rm <name>", "remove a name", `Remove the alias with this name, and print an "alias_removed" line.`,
		nargs(1, 1), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if err := b.RemoveAlias(c.Context(), args[0]); err != nil {
				return err
			}
			return e.out(line("alias_removed", "name", args[0]))
		})
}

func (e *cli) aliasListCmd() *cobra.Command {
	return command("list", "list the names, one \"alias\" line each",
		`List the names this mailbox has given its peers, one "alias" line each, with the
name and its address.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			as, err := b.Aliases(c.Context())
			for _, a := range as {
				if werr := e.out(line("alias", "name", a.Name, "address", a.Address)); werr != nil && err == nil {
					err = werr
				}
			}
			return err
		})
}

func (e *cli) flushCmd() *cobra.Command {
	return command("flush", "post again every message still in the outbox",
		`Post again every message still in the outbox, each one signed and encrypted
afresh as send does it, and print a "sent" line for each message the server
accepts, as send does. A message that can never be delivered moves to
outbox/failed/.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			ss, err := b.Flush(c.Context())
			n := namesOf(c.Context(), b)
			var aclErr error
			for _, s := range ss {
				if werr := e.out(sent(s, n)); werr != nil && err == nil {
					err = werr
				}
				aclErr = s.ACLErr
			}
			if err != nil {
				return err
			}
			return e.aclNotSent(aclErr)
		})
}

// aclNotSent says that a post went ahead though the mailbox's ACL was not taken,
// since the one its server last took admits the reply: the mailbox needs a look.
func (e *cli) aclNotSent(err error) error {
	if err == nil {
		return nil
	}
	if werr := e.out(line("acl_sync_failed", failure(fmt.Errorf("the mailbox's server takes the reply under the ACL it last took: the next send or recv sends this mailbox's, and dromas status says whether it went through: %w", err))...)); werr != nil {
		return werr
	}
	return &needsALook{}
}

func (e *cli) renewCmd() *cobra.Command {
	return command("renew", "publish a new prekey now",
		`Publish a new prekey now, signed by the identity, or under a new working key and
certificate once the mailbox has published one under a certificate.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			p, err := b.Rekey(c.Context())
			if p != nil {
				// Published, whatever fault of this mailbox came after.
				if werr := e.out(prekey(p)); err == nil {
					err = werr
				}
			}
			if err != nil {
				return err
			}
			nm, err := b.NotMade(c.Context())
			if err != nil || nm == nil {
				return err
			}
			if err := e.out(notMadeLine(nm)); err != nil {
				return err
			}
			return &needsALook{}
		})
}

// shortLifetime is the prekey lifetime under which setting one warns: with
// less, a recv once a day does not renew a prekey while the room a renewal
// leaves is left (client.RecvWithin): the day between two recv runs, the
// day recv waits to renew, the hour for the renewal's own calls and the hour
// for clocks.
const shortLifetime = 2*24*time.Hour + time.Hour + protocol.MaxSkew

func (e *cli) lifetimeCmd() *cobra.Command {
	return command("lifetime", "print how long each new prekey is valid",
		fmt.Sprintf(`Print how long each new prekey of this mailbox is valid, which the config file's
prekey-lifetime sets: from %s to %s, the default. A time is written as sshd
writes one: numbers, each with a unit, s, m, h, d or w, added together, as in
2d2h or 90m. A number without a unit is seconds.

recv renews the prekey once it is a day old, or sooner when the lifetime is too
short for that. With a lifetime of %s or more, a recv once a day renews the
prekey in time. Under that, this command says how often recv must run: otherwise
the prekey expires, or comes so near expiry that a sender whose clock is ahead
refuses it, and senders are refused until the mailbox renews.

A lower lifetime takes effect at the next recv, which renews a prekey made for
longer.`, formatTime(client.MinPrekeyLifetime), formatTime(protocol.PrekeyLifetime), formatTime(shortLifetime)),
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			d, err := b.PrekeyLifetime(c.Context())
			if err != nil {
				return err
			}
			var warning any
			if d < shortLifetime {
				warning = fmt.Sprintf("WARNING: a lifetime under %s risks a single missed daily check-in leaving this mailbox unable to receive messages. At %s, run dromas recv at least every %s.", formatTime(shortLifetime), formatTime(d), formatTime(client.RecvWithin(d)))
			}
			return e.out(line("prekey_lifetime", "lifetime_seconds", int64(d/time.Second), "lifetime", formatTime(d), "detail", warning))
		})
}

// timeUnits are the units of a time as sshd reads one (sshd_config(5), TIME
// FORMATS), in either case.
var timeUnits = map[byte]time.Duration{'s': time.Second, 'm': time.Minute, 'h': time.Hour, 'd': 24 * time.Hour, 'w': 7 * 24 * time.Hour}

// parseTime reads a time as sshd does (sshd_config(5), TIME FORMATS): a
// sequence of numbers, each with a unit or none for seconds, added together,
// as in 600, 10m or 2d1h. It is OpenSSH's convtime, as 10.3p1 to 10.6p1 have
// it (misc.c, convtime_double; #193): each number in decimal digits alone,
// any number of them, a fraction only on seconds and only with a digit after
// its point, seconds named once, and the sum truncated to whole seconds, at
// most INT_MAX of them.
//
// It differs from convtime in one place, kept as it is (#389): a fraction
// below binary64's smallest normal, 2.2250738585072014e-308, so with some
// 308 digits after its point, which strconv.ParseFloat reads as a subnormal
// or zero, and convtime refuses wherever its libc's strtod sets ERANGE for
// an underflow (POSIX.1-2024, strtod). Which such fractions underflow is the
// libc's, glibc alone measured, so no code here follows one. Such a fraction
// is under a second, which the truncation drops: what parseTime takes there
// is the seconds the rest of the time names, every time convtime takes reads
// as the same seconds here, and the only time parseTime reads is one the
// mailbox's owner writes (prekey-lifetime).
func parseTime(s string) (time.Duration, error) {
	no := fmt.Errorf("%.40q is no time: one is numbers, each with its unit (s, m, h, d or w), as in 2d1h", s)
	if s == "" {
		return 0, no
	}
	total, seconds := 0.0, false
	for p := 0; p < len(s); {
		i := p
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i < len(s) && s[i] == '.' {
			i++
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		}
		num := s[p:i]
		if strings.Trim(num, ".") == "" { // no digit: what strtod reads none of
			return 0, no
		}
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, no
		}
		unit := 1.0
		switch {
		case i == len(s) || s[i] == 's' || s[i] == 'S':
			if seconds {
				return 0, no
			}
			seconds = true
		default:
			u, ok := timeUnits[s[i]|0x20]
			if !ok || u == time.Second {
				return 0, no
			}
			unit = u.Seconds()
		}
		if strings.Contains(num, ".") && (unit > 1 || num[len(num)-1] == '.') {
			return 0, no
		}
		total += v * unit
		if p = i; p < len(s) {
			p++
		}
	}
	if total > math.MaxInt32 {
		return 0, fmt.Errorf("%.40q is longer than any prekey lifetime", s)
	}
	return time.Duration(int64(total)) * time.Second, nil
}

// formatTime writes d as parseTime reads it, in days, hours, minutes and
// seconds, as in 1d1h or 8d.
func formatTime(d time.Duration) string {
	var b strings.Builder
	for _, u := range []struct {
		d time.Duration
		c string
	}{{24 * time.Hour, "d"}, {time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if n := d / u.d; n > 0 {
			fmt.Fprintf(&b, "%d%s", n, u.c)
			d -= n * u.d
		}
	}
	if b.Len() == 0 {
		return "0s"
	}
	return b.String()
}

func (e *cli) switchCmd() *cobra.Command {
	return command("switch <address>", "renew under a certificate, permanently; the address in full confirms it",
		`Publish a new prekey now under a new working key and certificate, as dromas init
--offline registers one. From then on, the mailbox signs under certificates
permanently, because its server and its correspondents accept no statement its
identity signs below one. Messages its identity signed stay valid.

Type the mailbox's address in full as the confirmation.`,
		checked(nargs(1, 1), func(c *cobra.Command, args []string) error {
			if _, err := protocol.ParseAddress(args[0]); err != nil {
				return wrong(c, "%s takes this mailbox's address, written in full", c.CommandPath())
			}
			return nil
		}), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if a, _ := protocol.ParseAddress(args[0]); a != b.Address() {
				return errors.New("that is not this mailbox's address: nothing was done. The switch takes this mailbox's own address, in full, as its confirmation")
			}
			p, err := b.RenewUnderCertificate(c.Context())
			if p != nil {
				if werr := e.out(prekey(p)); werr != nil {
					return werr
				}
			}
			return err
		})
}

func (e *cli) revokeEarlierCmd() *cobra.Command {
	return command("revoke-earlier", "revoke every certificate before a new one, then publish a new prekey under it",
		`For a mailbox under a certificate whose working key may have been stolen: make a
new prekey under a new working key and certificate, revoke every certificate
before that one, and then publish the new prekey.

The address continues. A message this mailbox sent that is still unread is
refused along with the rest, so send it again. Until the server accepts the new
prekey, the mailbox sends no messages, and once the server holds the revocation,
the mailbox receives none there either.

A copy of the whole mailbox directory holds the identity too, and then the
remedy is to revoke the address.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			r, err := b.RenewAndRevoke(c.Context())
			return e.remedied(b.Address(), r, err)
		})
}

// remedied prints what the remedy of mailbox own did, r and err as
// RenewAndRevoke returned them, and returns the command's error: what became
// of the new prekey first, then of the revocation, then a fault of the mailbox
// after what a server took, which is never dropped (#110, #383). What a
// server took is said as taken. A mailbox shown its own revocation says that
// alone (#326), and RenewAndRevoke's error is then that revocation
// (outcome), so nothing is dropped there.
func (e *cli) remedied(own protocol.Address, r *client.Remedy, err error) error {
	if r == nil {
		var gone *client.RevokedError
		if errors.As(err, &gone) {
			return err
		}
		return fmt.Errorf("no new prekey, and nothing is revoked: %w", err)
	}
	// A mailbox shown its own revocation before its renewal was taken says
	// that alone: it sends nothing again and renews nothing (#326).
	if gone := (*client.RevokedError)(nil); errors.As(r.NotRenewed, &gone) && gone.Own {
		return r.NotRenewed
	}
	if r.Unread != nil {
		if err := e.out(line("lookup_failed", failure(fmt.Errorf("this mailbox's server could not be read, or served what this mailbox does not take, so the new certificate was numbered from what this mailbox knows: %w", r.Unread))...)); err != nil {
			return err
		}
	}
	if r.Prekey != nil {
		if err := e.out(prekey(r.Prekey)); err != nil {
			return err
		}
	}
	if r.Revoked != nil {
		const resend = "A message this mailbox sent before now, and that its reader has not read yet, was signed under a certificate just revoked and will be refused: send it again."
		if err := e.out(line("certificates_revoked", "address", own, "revocation_serial", r.Revoked.Through, "detail", resend)); err != nil {
			return err
		}
	}
	dark := fmt.Sprintf("Every certificate before %d is revoked, the one this mailbox was using among them, so it sends no messages, and its server takes none for it, until the new prekey is taken: dromas prekey renew sends it again, and so does dromas recv", r.Below)
	// What a line printed above has not said, the prekey's first.
	var said error
	switch {
	case r.NotRenewed == nil && r.NotRevoked == nil && r.Revoked == nil:
		if err == nil {
			return e.out(line("no_earlier_certificate", "detail", "no earlier certificate to revoke"))
		}
	case r.NotRenewed == nil && r.NotRevoked == nil:
	case r.NotRenewed == nil:
		said = fmt.Errorf("the new prekey is published, and the earlier certificates are NOT revoked: %w. dromas cert revoke-earlier revokes them", r.NotRevoked)
	case r.NotRevoked == nil:
		said = fmt.Errorf("the new prekey is NOT published: %w. %s", r.NotRenewed, dark)
	default:
		said = fmt.Errorf("the new prekey is NOT published: %w, and nothing is revoked: %v. dromas cert revoke-earlier tries both again", r.NotRenewed, r.NotRevoked)
	}
	// Then the fault of this mailbox after what a server took, in every case.
	switch {
	case said == nil:
		return err
	case err == nil:
		return said
	}
	return fmt.Errorf("%w. %w", said, err)
}

func (e *cli) publishRevokedCmd() *cobra.Command {
	return command("publish-revoked [<file>]", "send a revocation of certificates to the server again",
		`Send a record that carries a revocation of certificates, and no revocation of an
address, to the server, and keep it. The record is the file named, or without
one, the record this mailbox keeps of its own certificates when a server did not
accept it.

publish-revoked refuses a record that ends an address: dromas revocation publish
takes that one, with the address in full.`,
		nargs(0, 1), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			var rec *protocol.Revocations
			if len(args) == 1 {
				rec, err = readRecord(args[0])
			} else {
				rec, err = b.CertificatesRecord(c.Context())
				if err == nil && rec == nil {
					err = errors.New("this mailbox holds no revocation of its certificates: dromas cert revoke-earlier makes one")
				}
			}
			if err != nil {
				return err
			}
			// What ends an address goes out only with that address written
			// in full, as its confirmation.
			if len(rec.Revocation) > 0 {
				return errors.New("the record carries a revocation of its address, which nothing undoes: nothing was published. dromas revocation publish --record <file> <address> publishes one, with the address in full as the confirmation")
			}
			a, _, err := b.KeepRevocation(c.Context(), rec)
			if err != nil {
				return err
			}
			certs, err := protocol.ParseRevokedCertificates(rec.RevokedCertificates)
			if err != nil {
				return err
			}
			if _, err := b.Publish(c.Context(), rec); err != nil {
				return err
			}
			through, said := certs.Through, "published to the server"
			if a == b.Address() {
				// A server that held a higher one showed it, and the mailbox keeps
				// that one.
				if held, err := b.CertificatesRecord(c.Context()); err == nil && held != nil {
					if h, err := protocol.ParseRevokedCertificates(held.RevokedCertificates); err == nil && h.Through > through {
						through, said = h.Through, "the server holds a revocation of this mailbox's certificates through a higher serial, which covers this one: this mailbox keeps that one"
					}
				}
			}
			if err := e.out(line("certificates_revoked", "address", a, "revocation_serial", through, "detail", said)); err != nil {
				return err
			}
			// A server that holds the address revoked took the record as done
			// once its revocation verified, and the mailbox keeps that one.
			if a != b.Address() {
				return nil
			}
			st, err := b.Status(c.Context())
			if err != nil || !st.Revoked {
				return err
			}
			return e.out(line("revoked", "address", a, "detail", "the server holds this address revoked, and the revocation verifies: this mailbox signs nothing more"))
		})
}

func (e *cli) adoptCmd() *cobra.Command {
	return command("join", "join the account an adopt key names, reading the key from stdin",
		`Join the account that an adopt key names, which can lift this mailbox's limits.
dromas reads the key from stdin, never from the command line, where ps, a
shell's history and an agent's transcript would keep it: dromas account join <
key.txt.

dromas checks what it read against the adopt key format before it sends
anything, so a wrong file goes nowhere, and no error prints the key.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			key, err := adoptKey(e.stdin)
			if err != nil {
				return err
			}
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			acc, err := b.Adopt(c.Context(), key)
			if err != nil {
				return err
			}
			return e.out(line("account", "account", acc))
		})
}

// adoptKey reads an adopt key from r: one line, its line feed optional, of
// an adopt key's form (PROTOCOL.md §11.16), "dra1_" and base64url. Anything
// else is refused in words that never quote it, since it may be the key, or
// a secret piped in by mistake.
func adoptKey(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(maxAdoptKey+len("\r\n")+1)))
	if err != nil {
		return "", err
	}
	key := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
	if len(key) > maxAdoptKey || !adoptKeyForm.MatchString(key) {
		return "", errors.New("stdin does not hold an adopt key, one line of \"dra1_\" and base64url: nothing is sent, and what was read is not shown")
	}
	return key, nil
}

// adoptKeyForm is an adopt key's form, "dra1_" and base64url without
// padding, of at least one byte: groups of four characters, then two or
// three; and maxAdoptKey the most bytes one may be (PROTOCOL.md §11.16).
var adoptKeyForm = regexp.MustCompile(`\Adra1_(?:[A-Za-z0-9_-]{4})*(?:[A-Za-z0-9_-]{4}|[A-Za-z0-9_-]{2,3})\z`)

const maxAdoptKey = 1024

func (e *cli) leaveCmd() *cobra.Command {
	return command("leave", "leave the mailbox's account", `Leave the mailbox's account.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			if err := b.Leave(c.Context()); err != nil {
				return err
			}
			return e.out(line("account", "account", json.RawMessage("null"), "detail", "in no account"))
		})
}

func (e *cli) makeCmd() *cobra.Command {
	return command("make", "print this mailbox's revocation record, and publish nothing",
		`Print this mailbox's revocation record, one "revocation_record" line, and
publish nothing. Anyone who publishes that record permanently ends this address,
without the key: keep it where only you can read it, somewhere other than this
mailbox, for the day the mailbox is lost (umask 077; dromas revocation make >
<file>).

The line is the record exactly as every server accepts it: dromas revocation
publish --record publishes it, and so does a PUT of it to
<server>/v1/<address>/revocation.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			rec, err := b.Revocation(c.Context())
			if err != nil {
				return err
			}
			// The record names its own type, which it writes first, as every
			// line has its type: the line is the record.
			r, err := json.Marshal(rec)
			if err != nil {
				return err
			}
			return e.out(r)
		})
}

func (e *cli) publishCmd() *cobra.Command {
	var record string
	const how = "dromas revocation publish takes the 66-character address, in full: it is the confirmation, and a revocation cannot be undone"
	c := command("publish [flags] <address>", "revoke this mailbox's address, PERMANENTLY",
		`Revoke this mailbox's address, PERMANENTLY: nothing undoes it. Type the address
in full as the confirmation.

--record <file> publishes a record made earlier by dromas revocation make, and
needs no mailbox here. The record goes to the server, and the command fails
unless the server accepted it: run it again.`,
		func(c *cobra.Command, args []string) error {
			if len(args) != 1 {
				return wrong(c, "%s", how)
			}
			return nil
		}, func(c *cobra.Command, args []string) error {
			a, err := protocol.ParseAddress(args[0])
			if err != nil {
				return errors.New(how)
			}
			// The mailbox here, if there is one, keeps the record once the server
			// took it. With --record none is needed.
			b, openErr := e.mailbox()
			var rec *protocol.Revocations
			if record != "" {
				if rec, err = readRecord(record); err != nil {
					return err
				}
				if protocol.AddressOf(rec.Identity) != a {
					return errors.New("the record is a revocation of another address than the one named: nothing was published")
				}
			} else {
				if openErr != nil {
					return openErr
				}
				if b.Address() != a {
					return errors.New("that is not this mailbox's address: nothing was published. dromas revocation publish takes this mailbox's own address, or a record of another with --record")
				}
				if rec, err = b.Revocation(c.Context()); err != nil {
					return err
				}
			}
			var perr error
			if openErr == nil {
				_, perr = b.Revoke(c.Context(), rec)
			} else {
				_, perr = client.Revoke(c.Context(), e.opt, rec)
			}
			if perr != nil {
				return perr
			}
			// Once the server has taken it, it is out. If the mailbox it is about is
			// the one here, it keeps it too.
			if openErr == nil && b.Address() == a {
				if _, _, err := b.KeepRevocation(c.Context(), rec); err != nil {
					return err
				}
			}
			return e.out(line("revoked", "address", a, "detail", "published to the server"))
		})
	c.Flags().StringVar(&record, "record", "", "publish the record in this `file`, made earlier by dromas revocation make, in place of signing one with this mailbox's identity")
	return c
}

func (e *cli) keepCmd() *cobra.Command {
	return command("keep <file>", "keep a revocation that someone gave you",
		`Check and keep a revocation of another mailbox that someone gave you (a record
as dromas revocation make prints it). This mailbox then sends that address
nothing and accepts nothing from it. A record may carry a revocation of the
mailbox's certificates, which is kept the same way.

Each part that verifies is kept. When another part does not verify, keep reports
what it kept, names the part that failed, and ends with that failure.`,
		nargs(1, 1), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			rec, err := readRecord(args[0])
			if err != nil {
				return err
			}
			a, held, err := b.KeepRevocation(c.Context(), rec)
			if held != nil {
				if serr := e.sayHeld(a, held); serr != nil {
					return serr
				}
			}
			return err
		})
}

func (e *cli) askCmd() *cobra.Command {
	return command("ask <link, address or alias>", "ask the server what it holds about a mailbox, and keep what verifies",
		`Ask the server what it holds about a mailbox, and keep what verifies. send does
this on its own before it posts.`,
		nargs(1, 1), func(c *cobra.Command, args []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			a, err := b.Destination(args[0])
			if err != nil {
				return err
			}
			held, err := b.Ask(c.Context(), a)
			if err != nil {
				return err
			}
			return e.sayHeld(a, held)
		})
}

// sayHeld says what the mailbox holds of an address, which is what it checks a
// send and a read against. A server's "none" is its word, and is said as that.
func (e *cli) sayHeld(a protocol.Address, held *client.Held) error {
	switch {
	case held.Revoked:
		return e.out(line("revoked", "address", a))
	case held.Certificates != nil:
		return e.out(line("certificates_revoked", "address", a, "revocation_serial", held.Certificates.Through))
	}
	return e.out(line("not_revoked", "address", a, "detail", "no revocation shown. That is the server's word, and it can keep a record back: a record from the mailbox's owner settles it (dromas revocation keep <file>)."))
}

// sent is the line for a message the server took, with the mailbox's name for
// its recipient (names).
func sent(s *client.Sent, n names) []byte {
	return line("sent", "id", s.ID, "msg_id", s.MsgID, "to", s.To, "alias", n.of(s.To))
}

// prekey is the line for a prekey this mailbox published.
func prekey(p *protocol.Prekey) []byte {
	return line("prekey", "serial", p.Serial, "not_after", p.NotAfter.UTC().Format(protocol.TimeLayout))
}

// maxRecord bounds a revocation record read from a file: one is under a
// kilobyte.
const maxRecord = 64 << 10

// notMadeLine is the line that says the mailbox's server served a statement in
// its name that it did not make.
// A statement the identity signed has no certificate, and none is named; nor
// is a renewal above it before its server took one.
func notMadeLine(nm *client.NotMade) []byte {
	var serial, own, renewed any
	if nm.CertificateSerial != 0 {
		serial, own = nm.CertificateSerial, nm.OwnCertificate
	}
	if nm.RenewedAbove != 0 {
		renewed = nm.RenewedAbove
	}
	return line("prekey_not_made", "serial", nm.Serial, "certificate_serial", serial, "own_certificate", own, "renewed_above", renewed, "read_at", nm.At, "detail", nm.Words())
}

// readRecord reads a revocation record: the JSON dromas revocation make
// prints. What it holds is checked by whoever uses it.
func readRecord(name string) (*protocol.Revocations, error) {
	f, err := os.Open(name) //nolint:gosec // a file the caller named
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read only
	data, err := io.ReadAll(io.LimitReader(f, maxRecord+1))
	if err != nil {
		return nil, err
	}
	var rec protocol.Revocations
	if len(data) > maxRecord || protocol.Unmarshal(data, &rec) != nil || len(rec.Identity) == 0 || (len(rec.Revocation) == 0 && len(rec.RevokedCertificates) == 0) {
		return nil, errors.New("the file is not a revocation record: one is a line of JSON with \"identity\" and \"revocation_signature\", as dromas revocation make prints, or with a revocation of its certificates")
	}
	return &rec, nil
}
