package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// The identity's key is in the mailbox's own agent, never the shell's by
// default, since the shell's is the one ssh forwards; or in its file, unlocked
// by an askpass program or at a terminal. The agent is stock ssh-agent, which
// dromas agent start runs, and dromas agent add loads the key with stock
// ssh-add, each saying the line it runs.

// agentDefaultWords is the default socket, as help says it.
const agentDefaultWords = "$XDG_RUNTIME_DIR/dromas/agent.sock, or dromas/agent.sock in the user's cache directory, and none on Windows"

// defaultAgent is the default socket of the mailbox's agent: one for the user,
// apart from the shell's.
func defaultAgent() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, "dromas", "agent.sock")
	}
	if d, err := os.UserCacheDir(); err == nil && filepath.IsAbs(d) {
		return filepath.Join(d, "dromas", "agent.sock")
	}
	return ""
}

// agentSocket reads an agent setting as ssh_config(5) reads IdentityAgent: none
// for no agent, SSH_AUTH_SOCK or $NAME for the socket an environment variable
// names, and otherwise a path, relative ones under rel.
func agentSocket(v, rel string) (string, error) {
	switch {
	case v == "none":
		return "", nil
	case v == "SSH_AUTH_SOCK":
		return os.Getenv("SSH_AUTH_SOCK"), nil
	case strings.HasPrefix(v, "$"):
		return os.Getenv(v[1:]), nil
	}
	p, err := expand(v, rel)
	if err != nil {
		return "", err
	}
	return filepath.Abs(p)
}

// resolveIdentityKey reads where the identity's key comes from: the agent, the
// askpass program, whether a terminal is asked, and AddKeysToAgent, each flag,
// then environment, then file, then default.
func (cfg *config) resolveIdentityKey(g *globals, fc *fileConfig, rel string) error {
	bad := func(key, format string, a ...any) error {
		return &configError{file: cfg.file, key: key, msg: fmt.Sprintf(format, a...)}
	}
	pick := func(flag, env, file string) (v, from, r string) {
		switch e := os.Getenv(env); {
		case flag != "":
			return flag, "flag", "."
		case e != "":
			return e, "environment", "."
		case file != "":
			return file, "file", rel
		}
		return "", "default", "."
	}

	v, from, r := pick(g.agent, "DROMAS_AGENT", fc.Agent)
	if v == "" {
		cfg.agent = defaultAgent()
	} else {
		socket, err := agentSocket(v, r)
		if err != nil {
			return bad("agent", "agent: %v", err)
		}
		cfg.agent = socket
	}
	cfg.values["agent"] = setting{omitNone(cfg.agent), from}

	if v, from, r = pick(g.askpass, "DROMAS_ASKPASS", fc.Askpass); v != "" {
		// A bare name is found on PATH when it runs, as ssh runs SSH_ASKPASS.
		if strings.ContainsAny(v, `/\`) || strings.HasPrefix(v, "~") {
			p, err := expand(v, r)
			if err != nil {
				return bad("askpass", "askpass: %v", err)
			}
			v = p
		}
		cfg.askpass = v
		cfg.values["askpass"] = setting{v, from}
	}

	cfg.noPrompt, from = false, "default"
	switch e := os.Getenv("DROMAS_NO_PROMPT"); {
	case g.noPromptSet:
		cfg.noPrompt, from = g.noPrompt, "flag"
	case e != "":
		b, err := strconv.ParseBool(e)
		if err != nil {
			return bad("no-prompt", "$DROMAS_NO_PROMPT is true or false, and %.40q is not", e)
		}
		cfg.noPrompt, from = b, "environment"
	case fc.NoPrompt != nil:
		cfg.noPrompt, from = *fc.NoPrompt, "file"
	}
	cfg.values["no-prompt"] = setting{strconv.FormatBool(cfg.noPrompt), from}

	v, from, _ = pick(g.addKeysToAgent, "DROMAS_ADD_KEYS_TO_AGENT", fc.AddKeysToAgent)
	switch v {
	case "", "no":
		v = "no"
	case "yes":
		cfg.addKeys = true
	default:
		d, err := parseTime(v)
		if err != nil || d < time.Second {
			return bad("add-keys-to-agent", "add-keys-to-agent is no, yes or a time of a second or more, and %.40q is not", v)
		}
		cfg.addKeys, cfg.agentLife = true, d
	}
	cfg.values["add-keys-to-agent"] = setting{v, from}
	return nil
}

// omitNone is a socket as dromas config says it: none where there is no agent.
func omitNone(socket string) string {
	if socket == "" {
		return "none"
	}
	return socket
}

// passphrase has the identity key file's passphrase for client.Options: from the
// askpass program where one is set, as ssh's SSH_ASKPASS_REQUIRE=force has it,
// and otherwise at the terminal, after a "waiting" line (askTerminal). It asks the terminal
// only where stdin is one, as a person's shell gives it and a program's call
// does not, so a model that runs dromas through a tool fails as identity_locked
// at once rather than waiting on a prompt nobody sees; and never under
// no-prompt, for a call through a terminal of its own. stdout is not looked
// at: what a command prints is the same everywhere.
func (e *cli) passphrase(file string) ([]byte, error) {
	prompt := fmt.Sprintf("Enter passphrase for %s: ", file)
	if e.cfg.askpass != "" {
		return askpass(e.cfg.askpass, prompt)
	}
	if !e.mayPrompt() {
		return nil, client.ErrNoPassphrase
	}
	// An interrupt already: no prompt, which nothing would restore.
	if err := e.ctx.Err(); err != nil {
		return nil, err
	}
	in, out, err := openTerminal()
	if err != nil {
		return nil, client.ErrNoPassphrase
	}
	fd := int(in.Fd()) //nolint:gosec // a file descriptor
	// Raw before the read starts, so an interrupt's restore comes after it.
	state, err := term.MakeRaw(fd)
	if err != nil {
		closeTerminal(in, out)
		return nil, client.ErrNoPassphrase
	}
	_, _ = fmt.Fprintf(e.stderr, "%s\n", line("waiting", "for", "passphrase", "identity_file", file, "detail",
		"waiting for the identity key's passphrase at the terminal: its key file has a passphrase, and the mailbox's agent holds no key of it"))
	pass, err := askTerminal(e.ctx, e.interrupt,
		func() ([]byte, error) { return readPassphrase(in, out, fd, state, prompt) },
		func() error {
			rerr := term.Restore(fd, state)
			_, _ = io.WriteString(out, "\n")
			return rerr
		})
	// A read an interrupt left blocked keeps the terminal open until the
	// process ends: on macOS, close(2) of a terminal waits for a read of it.
	if e.ctx.Err() == nil {
		closeTerminal(in, out)
	}
	return pass, err
}

// errInterrupted is a ^C at the passphrase's prompt, which a terminal in raw
// mode passes as a key rather than as an interrupt.
var errInterrupted = errors.New("interrupted at the passphrase's prompt")

// readPassphrase reads a passphrase at the terminal, in and out, whose
// descriptor is fd, already in raw mode, as ssh's readpassphrase(3) reads one:
// the prompt goes to the terminal, the line is read with echo off, and the
// terminal's settings, state, are restored after. term.Terminal reads it, in
// raw mode, where ^D on an empty line is the end of input and ^C a key, which
// it reads as errInterrupted.
func readPassphrase(in io.Reader, out io.Writer, fd int, state *term.State, prompt string) ([]byte, error) {
	keys := &keysRead{r: in}
	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{keys, out}, "")
	// term.Terminal wraps the prompt at 80 columns unless told the width.
	if w, h, err := term.GetSize(fd); err == nil && w > 0 && h > 0 {
		_ = t.SetSize(w, h)
	}
	pass, err := t.ReadPassword(prompt)
	rerr := term.Restore(fd, state)
	if err != nil {
		// term.Terminal ends the line itself only on Enter.
		_, _ = io.WriteString(out, "\n")
		if keys.interrupted {
			return nil, errInterrupted
		}
		return nil, err
	}
	if rerr != nil {
		return nil, rerr
	}
	return []byte(pass), nil
}

// keysRead is a terminal in raw mode as term.Terminal reads it: it notes a ^C,
// which term.Terminal reads as the end of input, as ^D.
type keysRead struct {
	r           io.Reader
	interrupted bool
}

func (k *keysRead) Read(p []byte) (int, error) {
	n, err := k.r.Read(p)
	if bytes.IndexByte(p[:n], 3) >= 0 {
		k.interrupted = true
	}
	return n, err
}

// askTerminal is the terminal's prompt, read reading the line (readPassphrase):
// an end of input, ^D, or an empty line is no passphrase, as ssh takes one, and
// a ^C at the prompt is an interrupt, which ends the command's context as one
// from a signal does. An interrupt from a signal, which ends ctx, restores the
// terminal's settings, echo among them, and ends the read at once, read left
// blocked until the process ends with the command.
func askTerminal(ctx context.Context, interrupt func(), read func() ([]byte, error), restore func() error) ([]byte, error) {
	type answer struct {
		pass []byte
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		pass, err := read()
		got <- answer{pass, err}
	}()
	select {
	case a := <-got:
		switch {
		case errors.Is(a.err, errInterrupted):
			interrupt()
			return nil, ctx.Err()
		case errors.Is(a.err, io.EOF), a.err == nil && len(a.pass) == 0:
			return nil, client.ErrNoPassphrase
		case a.err != nil:
			return nil, fmt.Errorf("the passphrase was not read from the terminal: %w", a.err)
		}
		return a.pass, nil
	case <-ctx.Done():
		if err := restore(); err != nil {
			return nil, fmt.Errorf("the terminal's settings were not restored: %w", errors.Join(ctx.Err(), err))
		}
		return nil, ctx.Err()
	}
}

// mayPrompt reports whether a passphrase may be asked at the terminal: stdin is
// one, and no-prompt is not set.
func (e *cli) mayPrompt() bool {
	return !e.cfg.noPrompt && isTerminal(e.stdin)
}

// isTerminal reports whether r is a terminal. A test that needs a terminal on
// stdin, which go test does not give, sets it (TestAgentAddAsksOnlyAtATerminal).
var isTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // a file descriptor
}

// openTerminal is the process's controlling terminal, which ssh asks for a
// passphrase on, apart from stdin and stdout: what it reads, and where its
// prompt goes. On Windows they are the console's input and output.
func openTerminal() (in, out *os.File, err error) {
	if runtime.GOOS != "windows" {
		tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		return tty, tty, err
	}
	if in, err = os.OpenFile("CONIN$", os.O_RDWR, 0); err != nil {
		return nil, nil, err
	}
	if out, err = os.OpenFile("CONOUT$", os.O_RDWR, 0); err != nil {
		_ = in.Close()
		return nil, nil, err
	}
	return in, out, nil
}

// closeTerminal closes what openTerminal opened.
func closeTerminal(in, out *os.File) {
	_ = in.Close()
	if out != in {
		_ = out.Close()
	}
}

// askpass runs program with prompt as its one argument, as ssh runs
// SSH_ASKPASS, and reads the passphrase from its stdout, without its final line
// break. What it writes to stderr is its own, and is not printed. A program
// that fails, or prints nothing, gives no passphrase, as ssh reads it.
func askpass(program, prompt string) ([]byte, error) {
	var out bytes.Buffer
	cmd := exec.Command(program, prompt) //nolint:gosec // the askpass program its owner set
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: the askpass program %s ended with %v", client.ErrNoPassphrase, program, err)
	}
	pass := out.Bytes()
	pass = bytes.TrimSuffix(pass, []byte("\n"))
	pass = bytes.TrimSuffix(pass, []byte("\r"))
	if len(pass) == 0 {
		return nil, fmt.Errorf("%w: the askpass program %s printed none", client.ErrNoPassphrase, program)
	}
	return pass, nil
}

// agentGroup is the agent group and its commands.
func (e *cli) agentGroup() *cobra.Command {
	return e.group("agent", "the mailbox's own ssh-agent, which holds its identity's key: start, add",
		`The identity's key may be kept in an ssh-agent of the mailbox's own, at the
socket --agent names, apart from the shell's agent, which ssh forwards. The key
file can then keep a passphrase. After a reboot the agent holds no key, so the
mailbox is locked until its owner runs dromas agent add again, or runs a
command with --askpass or at a terminal. A command that needs the key and has
it from none of these fails as "identity_locked". The agent's key is taken only
where it matches the identity's public key: the key file's where the file
carries one, and identity.pub's otherwise. A key the agent lists is the key
used, and the key file is not tried after it. The agent is stock ssh-agent, and dromas
agent start and dromas agent add each print the command they run.`,
		e.agentStartCmd(), e.agentAddCmd())
}

func (e *cli) agentStartCmd() *cobra.Command {
	return command("start", "run ssh-agent at the mailbox's agent socket, until it is stopped",
		`Run stock ssh-agent in the foreground at --agent's socket, as a service's
unit runs it: ssh-agent -D -a <socket>. It prints an "agent" line with the line
it runs first, makes the socket's directory, 0700, where it is missing, and
removes a socket there that no agent answers on. It refuses a socket an agent
already answers on. It sets no SSH_AUTH_SOCK: the shell's agent stays the
shell's.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			socket, err := e.agentSocket()
			if err != nil {
				return err
			}
			if conn, err := net.DialTimeout("unix", socket, 2*time.Second); err == nil {
				_ = conn.Close()
				return fmt.Errorf("an agent already answers at %s", socket)
			}
			if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
				return err
			}
			if fi, err := os.Lstat(socket); err == nil {
				if fi.Mode()&os.ModeSocket == 0 {
					return fmt.Errorf("%s is no socket: the agent's path names a file, which agent start never removes; set the agent to a path of its own", socket)
				}
				if err := os.Remove(socket); err != nil {
					return err
				}
			}
			args := []string{"-D", "-a", socket}
			if err := e.out(line("agent", "socket", socket, "command", shellLine(nil, "ssh-agent", args...))); err != nil {
				return err
			}
			cmd := exec.CommandContext(c.Context(), "ssh-agent", args...) //nolint:gosec // stock ssh-agent at the socket its owner set
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("ssh-agent ended: %w", err)
			}
			return nil
		})
}

func (e *cli) agentAddCmd() *cobra.Command {
	var life string
	c := command("add [flags]", "load the identity's key into the mailbox's agent, with ssh-add",
		`Run stock ssh-add on the identity key file, against the socket --agent names,
with the command's stdin: SSH_AUTH_SOCK=<socket> ssh-add <identity-file>.
Without --askpass, ssh-add asks for the key's passphrase at the terminal and
runs no askpass program (SSH_ASKPASS_REQUIRE=never); where the key file has a
passphrase that ssh-add could not ask for, since stdin is no terminal or
--no-prompt is set, agent add fails as "identity_locked" before ssh-add runs.
With --askpass, ssh-add runs that
program through dromas (SSH_ASKPASS=<dromas> SSH_ASKPASS_REQUIRE=force
DROMAS_ASKPASS=<program>), which gives ssh-add no passphrase after 3, as ssh's
NumberOfPasswordPrompts does; where no passphrase the askpass program gave
unlocked the key file, agent add fails as "identity_locked". Any other failure
of ssh-add is said with what ssh-add wrote to stderr. A revoked mailbox, which signs
nothing more, is refused before ssh-add runs. Before ssh-add runs, an
"agent_add" line gives the command it runs. Once the agent holds the key, an
"agent_added" line gives the mailbox's signer, as status names it.
--lifetime is ssh-add -t: when it ends, the agent drops the key. While the
agent lacks the key, and the key file has a passphrase, a mailbox whose
identity signs neither sends nor receives, and a mailbox under a certificate
renews nothing, but in a command run with --askpass or at a terminal.`,
		nargs(0, 0), func(c *cobra.Command, _ []string) error {
			b, err := e.mailbox()
			if err != nil {
				return err
			}
			socket, err := e.agentSocket()
			if err != nil {
				return err
			}
			// A revoked mailbox signs nothing more, so its key goes in no agent.
			st, err := b.Status(c.Context())
			if err != nil {
				return err
			}
			if st.Revoked {
				return &client.RevokedError{Address: b.Address(), Own: true}
			}
			file := b.Path("identity")
			env := []string{"SSH_AUTH_SOCK=" + socket}
			var tries string
			if e.cfg.askpass == "" {
				// Without --askpass, ssh-add reads the passphrase at the
				// terminal alone, and never runs an askpass program that the
				// environment names (SSH_ASKPASS, DISPLAY). A key file with
				// no passphrase asks nothing.
				if st.IdentityKey == client.KeyPassphrase && !e.mayPrompt() {
					return &client.IdentityLockedError{File: file, Agent: socket, Reason: "without --askpass, agent add runs ssh-add only where stdin is a terminal and no-prompt is not set, since ssh-add asks for the key file's passphrase at the terminal"}
				}
				env = append(env, "SSH_ASKPASS_REQUIRE=never")
			} else {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				dir, err := os.MkdirTemp("", "dromas-askpass")
				if err != nil {
					return err
				}
				defer os.RemoveAll(dir) //nolint:errcheck // the count of one run
				tries = filepath.Join(dir, "tries")
				env = append(env, "SSH_ASKPASS="+exe, "SSH_ASKPASS_REQUIRE=force", "DROMAS_ASKPASS="+e.cfg.askpass, triesEnv+"="+tries)
			}
			var args []string
			if life != "" {
				d, err := parseTime(life)
				if err != nil || d < time.Second {
					return wrong(c, "%s --lifetime takes a time of a second or more", c.CommandPath())
				}
				args = append(args, "-t", strconv.FormatInt(int64(d/time.Second), 10))
			}
			args = append(args, file)
			if err := e.out(line("agent_add", "socket", socket, "identity_file", file, "command", shellLine(env, "ssh-add", args...))); err != nil {
				return err
			}
			cmd := exec.CommandContext(c.Context(), "ssh-add", args...) //nolint:gosec // stock ssh-add on the mailbox's own key file
			// ssh-add reads a passphrase from the terminal itself. Its stdout
			// is its own, not this client's; what it writes to stderr is kept,
			// to say in the error why it failed. Its stdin is the command's,
			// which ssh-add reads to choose between the terminal and an
			// askpass program.
			var said bytes.Buffer
			cmd.Stdin = e.stdin
			cmd.Stderr = &said
			cmd.Env = append(os.Environ(), env...)
			if err := cmd.Run(); err != nil {
				// An askpass program that ran, and a failure after it: no
				// passphrase it gave, if any, unlocked the key file.
				if tries != "" && askedTimes(tries) > 0 {
					return &client.IdentityLockedError{File: file, Agent: socket, Reason: fmt.Sprintf("the askpass program gave no passphrase that unlocked the key file, in at most %d tries", askpassTries)}
				}
				if why := strings.TrimSpace(said.String()); why != "" {
					return fmt.Errorf("ssh-add did not load the key: %w: %s", err, why)
				}
				return fmt.Errorf("ssh-add did not load the key: %w", err)
			}
			st, err = b.Status(c.Context())
			if err != nil {
				return err
			}
			if st.IdentityKey != client.KeyInAgent {
				return fmt.Errorf("ssh-add ended, and the agent at %s holds no key of this identity", socket)
			}
			return e.out(line("agent_added", "socket", socket, "signer", st.Signer))
		})
	c.Flags().StringVar(&life, "lifetime", "", "how long the agent keeps the key, as ssh-add -t: a `time`")
	return c
}

// triesEnv names the file that counts the runs of agent add's askpass
// program (countedAskpass).
const triesEnv = "DROMAS_ASKPASS_TRIES"

// askpassTries is how many passphrases agent add takes from its askpass
// program: ssh's NumberOfPasswordPrompts default (ssh_config(5)).
const askpassTries = 3

// countedAskpassMain runs dromas as agent add's askpass program, where the
// environment names a count's file and the one argument is ssh-add's prompt,
// and returns its exit status.
func countedAskpassMain() (int, bool) {
	tries, ok := os.LookupEnv(triesEnv)
	if !ok || len(os.Args) != 2 {
		return 0, false
	}
	return countedAskpass(tries, os.Getenv("DROMAS_ASKPASS"), os.Args[1]), true
}

// countedAskpass is the askpass program agent add gives ssh-add with --askpass,
// a counter around the owner's program. ssh-add asks its askpass program again
// after each wrong passphrase, and stops only at an empty answer (ssh-add.c's
// add_file), so a program that prints a wrong one would be run without end.
// ssh-add runs the askpass program anew for each try, so the count is kept in
// the file tries, one byte a run, in a directory agent add made for the run and
// removes after it. Past askpassTries runs, or where the count cannot be kept,
// it answers empty, which ssh-add takes as no passphrase. Otherwise it runs
// program with prompt, as ssh runs SSH_ASKPASS, the program's stdout its own
// and its stderr discarded, and exits as the program did: ssh reads a failed
// program as no passphrase.
func countedAskpass(tries, program, prompt string) int {
	f, err := os.OpenFile(tries, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // agent add's own count
	if err != nil {
		return 0
	}
	_, werr := f.Write([]byte{'.'})
	if cerr := f.Close(); werr != nil || cerr != nil || askedTimes(tries) > askpassTries {
		return 0
	}
	cmd := exec.Command(program, prompt) //nolint:gosec // the askpass program its owner set
	cmd.Stdout = os.Stdout
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, triesEnv+"=") })
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// askedTimes is how many times countedAskpass ran with the count tries.
func askedTimes(tries string) int64 {
	fi, err := os.Stat(tries)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// agentSocket is the socket the agent commands use. On Windows, OpenSSH runs one
// agent for the machine, as a service on a named pipe, and a mailbox's own is
// not built there.
func (e *cli) agentSocket() (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("a mailbox's own agent is not built for Windows, whose OpenSSH runs one agent for the machine, as a service")
	}
	if e.cfg.agent == "" {
		return "", errors.New("no agent socket is set: --agent names one")
	}
	return e.cfg.agent, nil
}

// shellLine writes a command as a POSIX shell would read it: its environment,
// its name and its arguments, each quoted where it needs to be.
func shellLine(env []string, name string, args ...string) string {
	words := make([]string, 0, len(env)+1+len(args))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		words = append(words, k+"="+shellQuote(v))
	}
	words = append(words, shellQuote(name))
	for _, a := range args {
		words = append(words, shellQuote(a))
	}
	return strings.Join(words, " ")
}

// shellQuote quotes s for a POSIX shell where it holds anything but the
// characters a path or a flag is made of.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:=@+,%") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
