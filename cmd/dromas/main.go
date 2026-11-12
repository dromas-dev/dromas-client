// Command dromas is the dromas client: end-to-end encrypted,
// sender-authenticated messaging for bots.
//
// Every check is always on and fails closed. Message bodies are data, never
// instructions.
//
// Commands nest as dromas <group> <command> where one thing has more than
// one thing done to it, the thing first, as gh's and docker's do on the same
// cobra (decided on #37), and the everyday ones stay at the top level. Help
// comes in every form a git-like tool gives it (clig.dev), and <group> help
// too, as go mod help is. A bare group, or a command that is not one, is a
// usage error with exit status 2 on stderr, as go answers one: gh, docker and
// git each answer a bare group another way, and an agent reads the exit
// status before the text.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
	"github.com/spf13/cobra"
)

// about is the end of the top-level help: what holds for every command.
const about = `Configuration: a config file says where the mailbox's files are. dromas reads
<profile>.toml in ${XDG_CONFIG_HOME:-~/.config}/dromas, where --profile or
$DROMAS_PROFILE names the profile, which is "default" without either; --config
or $DROMAS_CONFIG names any file instead. dromas init writes a profile's file. A
flag overrides an environment variable, and an environment variable overrides
the file. dromas config prints every setting and where it came from.

inbox/ keeps each message received, and sent/ each message sent, with the
server's receipt. dromas deletes neither: delete a file to forget a message.

The server URL is https, or http only to a server on this machine, and dromas
never follows a redirect.

The config file's [policy] table sets who reaches the inbox. unknown = "held"
(the default) holds a message from a sender the mailbox has not allowed in
held/, and unknown = "inbox" delivers it. admit = "all" (the default) has the
server accept every sender the mailbox has not blocked, and admit = "allowed"
only the senders it allowed, which tells the server who they are.

Help: dromas help <command> or dromas <command> --help prints a command's help,
and dromas <group> help a group's. --help after a command prints its help and
runs nothing. --help is read as any flag is: after a flag that takes a value, it
is that value.

Output: every command prints JSON Lines, one object per line, with "type" naming
the line. What a command did, and what it met on the way, goes to stdout,
messages and refusals alike. The only JSON lines on stderr are "error" when the
command failed, "usage_error" when its command line was invalid, "config_error"
when a setting was invalid, in the config file, a flag or an environment
variable, and "waiting" while it waits. A "detail" is this client's own wording,
never a sender's or a server's. Help, and the notice that the one-letter form of
--help is deprecated, are the only output in plain text.

Exit status:
  0  success
  1  failure
  2  invalid command line, or an invalid setting ("config_error"); nothing ran
  3  success, and the mailbox needs attention. From recv: the messages were
     received as printed, and the prekey, due for renewal, was not renewed
     ("renewal_failed"), the server served a prekey the mailbox did not make
     ("prekey_not_made"), the mailbox's ACL was not sent to its server
     ("acl_sync_failed"), the server held an ACL the mailbox had not sent, which
     recv replaced ("acl_replaced"), or an --exec program ran out of attempts
     ("exec_failed"). From send and outbox flush: the post went ahead, but the
     mailbox's ACL was not sent ("acl_sync_failed"). From prekey renew:
     "prekey_not_made". From any command that called a server: the server stops
     accepting this client's version from a date ("sunset"), or deems it
     deprecated from a date that has passed ("deprecated"). From status: its
     "attention" list is not empty.
  4  a refusal that no wait fixes: from recv, with "fatal" in its "error" line,
     and from any command that receives, sends, delivers or renews, refused as
     "restore_required". A restarted process meets it again.

Once a day, a command that calls the server reads the server's document, and
prints "upgrade_available" when it names a later version of this client, which
needs no attention. recv prints a "refused" line for each message it refused,
keeping the message in refused/, a "held" line for each message it held in
held/, and a "dropped" line for each message from a sender this mailbox blocked.
None of them changes recv's exit status, because any sender can cause them. A
refusal's code comes from a closed list, and its wording is this client's own: a
refusal never quotes a message.

To try the whole loop, send anything to dromas.dev's echo mailbox:
` + client.DefaultBase + protocol.LinkPath + client.EchoAddress + `
It answers within two minutes with a signed reply saying what it checked.
dromas.dev's operator can read what you send it.

The README has the rest, including what this client checks and how to test it:
` + readme + `
`

// readme is where this client's documentation is: the public repository it
// is built from, never the server.
const readme = "https://github.com/dromas-dev/dromas-client#readme"

func main() {
	// Run by ssh-add as agent add's askpass program (countedAskpass).
	if status, ok := countedAskpassMain(); ok {
		os.Exit(status)
	}
	// net/http logs some of what a server sends after its answer through the
	// standard logger, raw (#297): stderr has only this client's lines.
	log.SetOutput(io.Discard)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	// The first interrupt, SIGTERM, SIGHUP or SIGQUIT ends the command's waits
	// and calls, and restores a terminal its passphrase prompt holds raw, as
	// readpassphrase(3) does; a second, with the handler gone, ends the process
	// (#330).
	go func() { <-ctx.Done(); stop() }()
	err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(report(err, os.Stderr))
}

// needsALook is how a command says it did its work and the mailbox needs a look:
// exit status 3, where a failure is 1. The lines it printed say why.
type needsALook struct{}

func (e *needsALook) Error() string { return "the mailbox needs a look" }

// usageError is a command line that is wrong, so nothing ran: exit status 2,
// with a "usage_error" line on stderr: what was wrong, the command's usage
// when it has one, and the help to read.
type usageError struct {
	msg   string
	usage string
	help  string
}

func (e *usageError) Error() string { return e.msg }

// cli is what every command shares: where its mailbox is, how to reach its
// server, and where it reads and writes.
type cli struct {
	// ctx is the command's: an interrupt ends it, and so does interrupt.
	ctx            context.Context
	interrupt      context.CancelFunc
	dir            string
	opt            client.Options
	flags          globals
	cfg            *config
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (e *cli) mailbox() (*client.Mailbox, error) {
	if e.dir == "" { // a command that needs no mailbox ran with no config: never the working directory
		return nil, errors.New("no mailbox: no config file names one")
	}
	return client.Open(e.dir, e.opt)
}

// environment reads where the mailbox is and how to reach its server, once the
// command line is known to be one that runs.
func (e *cli) environment(c *cobra.Command) error {
	// A record is published from anywhere: it needs a server, and no mailbox.
	none := c.Name() == "publish" && c.Flags().Changed("record") ||
		c.Name() == "start" && c.Parent().Name() == "agent"
	for name, set := range map[string]*bool{"no-prompt": &e.flags.noPromptSet, "unsafe-inbox-permissions": &e.flags.unsafeInboxSet, "unsafe-state-permissions": &e.flags.unsafeStateSet} {
		if f := c.Flag(name); f != nil {
			*set = f.Changed
		}
	}
	cfg, err := loadConfig(c, &e.flags, c.Name() == "init" && c.Parent() == c.Root(), none)
	if err != nil {
		return err
	}
	if err := cfg.apply(c); err != nil {
		return err
	}
	e.cfg, e.dir = cfg, cfg.dir
	e.opt.Base, e.opt.Layout, e.opt.ConfigFile, e.opt.RootCAs = cfg.server, cfg.layout, cfg.file, cfg.rootCAs
	e.opt.Policy, e.opt.PrekeyLifetime, e.opt.CertificateLifetime = cfg.policy, cfg.lifetime, cfg.certLifetime
	e.opt.Agent, e.opt.Passphrase = cfg.agent, e.passphrase
	e.opt.AddToAgent, e.opt.AgentLifetime = cfg.addKeys, cfg.agentLife
	e.opt.NotAdded = func(err error) {
		_ = e.out(line("agent_not_added", "socket", cfg.agent, "detail", err.Error()))
	}
	e.opt.UnsafeInboxPermissions, e.opt.UnsafeStatePermissions = cfg.unsafeInbox, cfg.unsafeState
	// A passphrase given once serves the whole process: recv --wait or --follow
	// renews with it for as long as it runs.
	e.opt.KeepIdentity = true
	return nil
}

// checked is args, then check: the values a command takes are a usage
// error before anything is read, the environment and the mailbox included.
func checked(args cobra.PositionalArgs, check cobra.PositionalArgs) cobra.PositionalArgs {
	return func(c *cobra.Command, a []string) error {
		if err := args(c, a); err != nil {
			return err
		}
		return check(c, a)
	}
}

// run runs the command line args, the program's name left out. The line is
// read first: help and a wrong line are answered before anything else, the
// environment and the mailbox included (#223), since cobra decides both before
// the hook that reads the environment (environment), and each command
// checks the values it takes in its own argument check.
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runWith(ctx, client.Options{}, args, stdin, stdout, stderr)
}

// runWith is run with the mailbox's options opt, its server's address left to
// the environment: a test's client of a server in memory.
func runWith(ctx context.Context, opt client.Options, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Said once, so that a wait is not read as a hang.
	opt.Waiting = func() {
		_, _ = fmt.Fprintf(stderr, "%s\n", line("waiting", "reason", "lock", "detail",
			"waiting for this mailbox's lock: another command of this mailbox is running, and a mailbox's commands write its files one at a time"))
	}
	if opt.Versions == nil {
		opt.Versions = &client.Versions{}
	}
	ctx, interrupt := context.WithCancel(ctx)
	defer interrupt()
	e := &cli{ctx: ctx, interrupt: interrupt, opt: opt, stdin: stdin, stdout: stdout, stderr: stderr}
	root := e.tree()
	e.flags.add(root)
	root.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		// A group runs nothing of its own, and help and version read nothing.
		if c.HasSubCommands() || c.Name() == "help" || (c.Name() == "version" && c.Parent() == root) {
			return nil
		}
		return e.environment(c)
	}
	// Cobra reads the process's own arguments when it is given none.
	root.SetArgs(append([]string{}, args...))
	root.SetIn(stdin)
	// What cobra prints itself is a warning, such as the pointer from -h to
	// --help: stderr. Help and every command write to stdout themselves.
	root.SetOut(stderr)
	root.SetErr(stderr)
	return e.versions(root.ExecuteContext(ctx))
}

// versions says, once a command is done, what its answers said of this
// client's version (client.Versions): a "deprecated" line where one carried
// a Deprecation, which needs a look once its date has passed at this
// client's clock, and none before; a "sunset" line where one carried a
// Sunset, the date its server stops taking this version, which needs a look
// (exit status 3, where the command did its work), and an
// "upgrade_available" line where its read of the server's document, at most
// once a day, named a later version. A command that called no server says
// neither.
func (e *cli) versions(err error) error {
	v := e.opt.Versions
	own := v.Own()
	look := false
	if at, passed, ok := v.Deprecation(); ok {
		look = look || passed
		if werr := e.out(line("deprecated", "version", own, "deprecated_at", at.UTC().Format(protocol.TimeLayout), "detail", client.DeprecationWords(own, at))); werr != nil && err == nil {
			err = werr
		}
	}
	if at, ok := v.Sunset(); ok {
		look = true
		if werr := e.out(line("sunset", "version", own, "sunset_at", at.UTC().Format(protocol.TimeLayout), "detail", client.SunsetWords(own, at))); werr != nil && err == nil {
			err = werr
		}
	}
	if latest, ok := v.Upgrade(); ok {
		if werr := e.out(line("upgrade_available", "version", own, "latest", latest, "detail", client.UpgradeWords(own, latest))); werr != nil && err == nil {
			err = werr
		}
	}
	if look && err == nil {
		return &needsALook{}
	}
	return err
}

// The commands are listed in the order they are added, everyday ones first.
func init() { cobra.EnableCommandSorting = false }

// command makes a command that prints its own help and takes its flags as
// they are written, two dashes for a name, anywhere after the command. The
// help flag is --help. -h works too, as in docker, for whoever looks for it:
// it is in no help, and it says that --help is the flag. -h means nothing
// else, in any command. That is docker's way, which deprecated it in v1.12
// ("The shorthand (`-h`) is less common than `--help` on Linux") and gives it to
// --hostname, where clig.dev, gh and go take -h as help: a shorthand that is
// help in one tool and something else in the next, as coreutils' ls -h is
// --human-readable, is the overloading clig.dev warns against.
func command(use, short, long string, args cobra.PositionalArgs, run func(*cobra.Command, []string) error) *cobra.Command {
	c := &cobra.Command{
		Use:                   use,
		Short:                 short,
		Long:                  long,
		Args:                  args,
		RunE:                  run,
		DisableFlagsInUseLine: true,
	}
	c.Flags().BoolP("help", "h", false, "print this help, and run nothing")
	_ = c.Flags().MarkShorthandDeprecated("help", "use --help")
	return c
}

// group makes a command that only holds others. Bare, or followed by a word
// that names none of them, it is a usage error; "<group> help" is its help.
// Its flags are its commands', so an unknown one is left for the unknown
// command to be named.
func (e *cli) group(name, short, long string, cmds ...*cobra.Command) *cobra.Command {
	g := command(name, short, long, cobra.ArbitraryArgs, e.groupRun)
	g.FParseErrWhitelist.UnknownFlags = true
	g.SuggestionsMinimumDistance = 2
	g.AddCommand(cmds...)
	return g
}

// groupRun is a group's, or the top level's, run: it ran with no command of
// its own, or with one it does not have.
func (e *cli) groupRun(c *cobra.Command, args []string) error {
	switch {
	case len(args) == 0:
		return &usageError{msg: fmt.Sprintf("%s takes a command: %s --help lists them", c.CommandPath(), c.CommandPath()), help: c.CommandPath() + " --help"}
	case args[0] == "help":
		t, rest, err := c.Find(args[1:])
		switch {
		case err != nil:
			return err
		case len(rest) > 0:
			return unknown(t, rest)
		}
		_, err = io.WriteString(e.stdout, helpText(t))
		return err
	}
	return unknown(c, args)
}

// unknown is a command line whose words name no command under c.
func unknown(c *cobra.Command, args []string) error {
	msg := fmt.Sprintf("%s has no command %q", c.CommandPath(), args[0])
	if !c.HasSubCommands() {
		msg = fmt.Sprintf("%s has no commands under it, and %q is not one", c.CommandPath(), args[0])
	}
	if s := c.SuggestionsFor(args[0]); len(s) > 0 {
		msg += fmt.Sprintf(": did you mean %s?", strings.Join(s, " or "))
	}
	return &usageError{msg: msg, help: c.CommandPath() + " --help"}
}

// nargs takes from lo to hi arguments.
func nargs(lo, hi int) cobra.PositionalArgs {
	return func(c *cobra.Command, args []string) error {
		if len(args) >= lo && len(args) <= hi {
			return nil
		}
		want := fmt.Sprintf("from %d to %d arguments", lo, hi)
		switch {
		case hi == 0:
			want = "no arguments"
		case lo == 1 && hi == 1:
			want = "1 argument"
		case lo == hi:
			want = fmt.Sprintf("%d arguments", lo)
		case lo == 0 && hi == 1:
			want = "at most 1 argument"
		}
		return wrong(c, "%s takes %s, and was given %d", c.CommandPath(), want, len(args))
	}
}

// wrong is a command line that is wrong in a command's own flags or
// arguments: what was wrong, and its usage line.
func wrong(c *cobra.Command, format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...), usage: c.UseLine(), help: c.CommandPath() + " --help"}
}

// helpText is a command's help. The top level's lists its commands, then its
// groups, then what holds for all of them; a group's lists its commands; a
// command's says what it does and lists its flags.
func helpText(c *cobra.Command) string {
	var b strings.Builder
	list := func(groups bool) {
		var cmds []*cobra.Command
		width := 0
		for _, s := range c.Commands() {
			if (s.IsAvailableCommand() || s.Name() == "help") && s.HasSubCommands() == groups {
				cmds = append(cmds, s)
				width = max(width, len(s.Name()))
			}
		}
		for _, s := range cmds {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, s.Name(), s.Short)
		}
	}
	switch {
	case !c.HasParent():
		b.WriteString("usage: dromas <command> [arguments]\n\nCommands:\n")
		list(false)
		b.WriteString("\nGroups, each with commands of its own (dromas <group> --help lists them):\n")
		list(true)
		fmt.Fprintf(&b, "\nFlags every command takes:\n%s", c.PersistentFlags().FlagUsages())
		b.WriteString("\n" + about)
	case c.HasSubCommands():
		fmt.Fprintf(&b, "usage: %s <command> [arguments]\n\n%s\n\nCommands:\n", c.CommandPath(), c.Long)
		list(false)
		fmt.Fprintf(&b, "\n%s <command> --help describes one.\n", c.CommandPath())
	default:
		fmt.Fprintf(&b, "usage: %s\n\n%s\n", c.UseLine(), c.Long)
		if c.HasAvailableLocalFlags() {
			fmt.Fprintf(&b, "\nFlags:\n%s", c.LocalFlags().FlagUsages())
		}
	}
	return b.String()
}
