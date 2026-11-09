package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"golang.org/x/crypto/ssh"
)

// askpassEnv, set, has the test binary run as an askpass program print its
// value (TestMain), and askpassRunsEnv names a file it notes each run in.
const (
	askpassEnv     = "DROMAS_TEST_ASKPASS_PRINTS"
	askpassRunsEnv = "DROMAS_TEST_ASKPASS_RUNS"
)

// encryptIdentity puts the identity file at file under passphrase, as
// ssh-keygen -p does, in the file itself, so that its permissions stay.
func encryptIdentity(t *testing.T, file, passphrase string) {
	t.Helper()
	raw, err := os.ReadFile(file) //nolint:gosec // the test's own
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(key, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_TRUNC, 0) //nolint:gosec // the test's own
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(pem.EncodeToMemory(block)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// shortDir is a directory whose path is short enough for a socket's (104
// bytes on macOS).
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dra")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// The agent setting is read as ssh_config's IdentityAgent, from the flag, then
// $DROMAS_AGENT, then the config file, where a relative path is under the
// file's directory, then the default, the user's own socket and never the
// shell's.
func TestTheAgentSettingIsReadAsIdentityAgent(t *testing.T) {
	cleanEnv(t)
	root := t.TempDir()
	file := writeConfig(t, "base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\nagent = \"a.sock\"\n")
	t.Setenv("DROMAS_CONFIG", file)
	t.Setenv("SSH_AUTH_SOCK", "/the/shells/agent")
	t.Setenv("OTHER_AGENT", "/another/agent")
	if v, _ := configLine(t); v["agent"] != (setting{filepath.Join(filepath.Dir(file), "a.sock"), "file"}) {
		t.Errorf("from the file: %+v", v["agent"])
	}
	t.Setenv("DROMAS_AGENT", "$OTHER_AGENT")
	if v, _ := configLine(t); v["agent"] != (setting{"/another/agent", "environment"}) {
		t.Errorf("from the environment: %+v", v["agent"])
	}
	for flag, want := range map[string]string{"none": "none", "SSH_AUTH_SOCK": "/the/shells/agent"} {
		if v, _ := configLine(t, "--agent", flag); v["agent"] != (setting{want, "flag"}) {
			t.Errorf("--agent %s: %+v", flag, v["agent"])
		}
	}
	t.Setenv("DROMAS_AGENT", "")
	if err := os.WriteFile(file, []byte("base-dir = "+tomlString(filepath.Join(root, "b"))+"\nmail-dir = "+tomlString(filepath.Join(root, "m"))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	want := setting{filepath.Join(runtimeDir, "dromas", "agent.sock"), "default"}
	if runtime.GOOS == "windows" {
		want = setting{"none", "default"}
	}
	if v, _ := configLine(t); v["agent"] != want {
		t.Errorf("by default: %+v, want %+v", v["agent"], want)
	}
}

// failed is the "error" line a command's failure prints.
func failed(t *testing.T, err error) map[string]any {
	t.Helper()
	var errs bytes.Buffer
	report(err, &errs)
	var l map[string]any
	if jerr := json.Unmarshal(errs.Bytes(), &l); jerr != nil {
		t.Fatalf("%v: %q", jerr, errs.String())
	}
	return l
}

// A mailbox whose key file has a passphrase signs only once something gives the
// passphrase: with no terminal, or prompting off, a renewal fails with
// identity_locked, naming the key file, and an askpass program unlocks it.
// status says the key file has a passphrase, and needs no look for it while no
// renewal is due.
func TestAKeyFileUnderAPassphraseIsLockedUntilAPassphraseIsGiven(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "identity")
	encryptIdentity(t, file, "correct horse")
	err := do(t.Context(), "prekey renew", []string{"--no-prompt"}, nil, io.Discard, io.Discard)
	if l := failed(t, err); l["code"] != client.IdentityLocked || l["identity_file"] != file {
		t.Errorf("a renewal with no passphrase: %v", l)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "battery staple")
	if l := failed(t, do(t.Context(), "prekey renew", []string{"--askpass", exe}, nil, io.Discard, io.Discard)); l["code"] != client.IdentityLocked {
		t.Errorf("a renewal with the wrong passphrase: %v", l)
	}
	t.Setenv(askpassEnv, "correct horse")
	if err := do(t.Context(), "prekey renew", []string{"--askpass", exe}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("a renewal with the passphrase: %v", err)
	}
	var out bytes.Buffer
	if err := do(t.Context(), "status", nil, nil, &out, io.Discard); err != nil {
		t.Errorf("status of a locked mailbox with no renewal due: %v", err)
	}
	if !strings.Contains(out.String(), `"identity_key":"passphrase"`) {
		t.Errorf("status: %s", out.String())
	}
}

// dromas agent start removes a stale socket at the agent's path and nothing
// else: a path that names a file is refused, and the file is left as it was.
func TestAgentStartRemovesOnlyASocket(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	notes := filepath.Join(shortDir(t), "notes.txt")
	if err := os.WriteFile(notes, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DROMAS_AGENT", notes)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	// Bounded, so that an agent start that took the path and ran ssh-agent
	// there ends, and fails the test, rather than running on.
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	want := "is no socket"
	if runtime.GOOS == "windows" {
		want = "not built for Windows" // the mailbox's own agent (TestAgentStartAndAddRunStockSSHAgent)
	}
	if err := do(ctx, "agent start", nil, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("agent start at a file's path: %v", err)
	}
	if b, err := os.ReadFile(notes); err != nil || string(b) != "kept\n" {
		t.Errorf("the file at the agent's path after agent start: %q, %v", b, err)
	}
}

// dromas agent start runs stock ssh-agent at the mailbox's socket, dromas
// agent add loads the key with stock ssh-add, its passphrase from --askpass,
// and the mailbox then signs through the agent, its key file locked and no
// passphrase given. On Windows both refuse: the mailbox's own agent is not
// built there.
func TestAgentStartAndAddRunStockSSHAgent(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	socket := filepath.Join(shortDir(t), "agent", "s")
	t.Setenv("DROMAS_AGENT", socket)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		for _, cmd := range []string{"agent start", "agent add"} {
			if err := do(t.Context(), cmd, nil, nil, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "not built for Windows") {
				t.Errorf("%s: %v", cmd, err)
			}
		}
		return
	}
	encryptIdentity(t, filepath.Join(dir, "identity"), "correct horse")
	ctx, stop := context.WithCancel(t.Context())
	var started bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- do(ctx, "agent start", nil, nil, &started, io.Discard) }()
	t.Cleanup(func() { stop(); <-done })
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("unix", socket); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no agent answers at %s: %s", socket, started.String())
		}
	}
	if !strings.Contains(started.String(), `"command":"ssh-agent -D -a `+socket+`"`) {
		t.Errorf("agent start's line: %s", started.String())
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "correct horse")
	var added bytes.Buffer
	if err := do(t.Context(), "agent add", []string{"--askpass", exe}, nil, &added, io.Discard); err != nil {
		t.Fatalf("agent add: %v: %s", err, added.String())
	}
	for _, want := range []string{`"command":"SSH_AUTH_SOCK=` + socket + ` SSH_ASKPASS=` + exe + ` SSH_ASKPASS_REQUIRE=force DROMAS_ASKPASS=` + exe + ` DROMAS_ASKPASS_TRIES=`, `"type":"agent_added"`} {
		if !strings.Contains(added.String(), want) {
			t.Errorf("agent add says no %s: %s", want, added.String())
		}
	}
	t.Setenv(askpassEnv, "")
	if err := do(t.Context(), "prekey renew", []string{"--no-prompt"}, nil, io.Discard, io.Discard); err != nil {
		t.Errorf("a renewal through the agent: %v", err)
	}
	var out bytes.Buffer
	_ = do(t.Context(), "status", nil, nil, &out, io.Discard) // its exit status is any attention's
	if !strings.Contains(out.String(), `"identity_key":"agent"`) {
		t.Errorf("status: %s", out.String())
	}
}

// fakeSSHAdd puts first in PATH an ssh-add that records, in the file it
// returns, what it was run with: SSH_ASKPASS_REQUIRE, SSH_ASKPASS, its
// arguments and its stdin; and fails, as ssh-add does when it loads no key.
func fakeSSHAdd(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "ran")
	script := "#!/bin/sh\n{ echo \"SSH_ASKPASS_REQUIRE=${SSH_ASKPASS_REQUIRE-unset}\"; echo \"SSH_ASKPASS=${SSH_ASKPASS-unset}\"; echo \"args=$*\"; cat; } > " + shellQuote(record) + "\necho 'fake ssh-add: refused' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh-add"), []byte(script), 0o700); err != nil { //nolint:gosec // a program the test runs
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// terminalStdin has isTerminal take stdin as a terminal, for one test: go test
// gives none.
func terminalStdin(t *testing.T) {
	t.Helper()
	was := isTerminal
	isTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { isTerminal = was })
}

// The terminal's prompt reads a passphrase as readpassphrase(3), which ssh
// reads one with: an end of input (^D) and an empty line are no passphrase; a
// ^C at the prompt ends the command's context, as an interrupt by a signal
// does; and an interrupt by a signal restores the terminal's settings, echo
// among them, and ends the read at once with the interrupt's error, a read
// still blocked. A terminal itself is a test by hand, under a pseudo-terminal:
// go test gives none.
func TestTheTerminalPromptReadsAsReadpassphrase(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		pass []byte
		err  error
		want []byte
		werr error
	}{
		"a passphrase":  {pass: []byte("correct horse"), want: []byte("correct horse")},
		"^D":            {err: io.EOF, werr: client.ErrNoPassphrase},
		"an empty line": {pass: []byte{}, werr: client.ErrNoPassphrase},
		"^C":            {err: errInterrupted, werr: context.Canceled},
	} {
		ctx, interrupt := context.WithCancel(t.Context())
		restored := false
		got, err := askTerminal(ctx, interrupt, func() ([]byte, error) { return c.pass, c.err }, func() error { restored = true; return nil })
		if !bytes.Equal(got, c.want) || !errors.Is(err, c.werr) || (c.werr == nil && err != nil) {
			t.Errorf("%s: %q, %v", name, got, err)
		}
		if restored || (ctx.Err() != nil) != errors.Is(c.werr, context.Canceled) {
			t.Errorf("%s: the settings restored by askTerminal: %v; the command's context: %v", name, restored, ctx.Err())
		}
		interrupt()
	}

	ctx, interrupt := context.WithCancel(t.Context())
	blocked := make(chan struct{})
	defer close(blocked)
	restored := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, err := askTerminal(ctx, interrupt, func() ([]byte, error) { <-blocked; return nil, io.EOF }, func() error { restored <- struct{}{}; return nil })
		done <- err
	}()
	interrupt()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("an interrupt at the prompt: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an interrupt did not end the prompt")
	}
	select {
	case <-restored:
	default:
		t.Error("an interrupt left the terminal's settings unrestored")
	}
}

// recv's renewal_failed line carries identity_locked where the identity's key
// was locked for the renewal, at each recv: a renewal the key was locked for is
// not held to the hour a server's refusal is.
func TestRecvSaysARenewalTheKeyWasLockedForAtEachRecv(t *testing.T) {
	ctx := context.Background()
	// Made 25 hours ago, by a clock the server shares (TestRecvSaysARenewalFailed).
	var ago atomic.Int64
	ago.Store(int64(25 * time.Hour))
	clock := func() time.Time { return time.Now().Add(-time.Duration(ago.Load())) }
	srv := fakeserver.New(t, clock)
	b, err := client.Init(ctx, t.TempDir(), client.Options{Base: srv.URL, Now: clock, WorkingKey: true})
	if err != nil {
		t.Fatal(err)
	}
	ago.Store(0)
	encryptIdentity(t, b.Path("identity"), "correct horse")
	useMailbox(t, b.Dir())
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	for i := range 2 {
		var out bytes.Buffer
		var look *needsALook
		if err := do(ctx, "recv", []string{"--no-prompt"}, nil, &out, io.Discard); !errors.As(err, &look) {
			t.Fatalf("recv %d: %v", i+1, err)
		}
		if ls := printed(t, out.String()); typesOf(ls) != "renewal_failed" || ls[0]["code"] != client.IdentityLocked {
			t.Errorf("recv %d: %s", i+1, out.String())
		}
	}
}

// An askpass program that fails, or prints nothing, gives no passphrase, as
// ssh reads it: the command fails as identity_locked.
func TestAnAskpassProgramThatGivesNothingLeavesTheIdentityLocked(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	encryptIdentity(t, filepath.Join(dir, "identity"), "correct horse")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "")
	if l := failed(t, do(t.Context(), "prekey renew", []string{"--askpass", exe}, nil, io.Discard, io.Discard)); l["code"] != client.IdentityLocked {
		t.Errorf("an askpass program that prints nothing: %v", l)
	}
	if runtime.GOOS == "windows" {
		return // no false(1)
	}
	if l := failed(t, do(t.Context(), "prekey renew", []string{"--askpass", "false"}, nil, io.Discard, io.Discard)); l["code"] != client.IdentityLocked {
		t.Errorf("an askpass program that fails: %v", l)
	}
}

// add-keys-to-agent with no agent at the socket fails nothing, as ssh's
// AddKeysToAgent: the command signs with the key the passphrase unlocked, and
// an "agent_not_added" line says the agent did not take it.
func TestAnAgentNotThereFailsNoCommandThatAddsTheKey(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	socket := filepath.Join(shortDir(t), "s")
	t.Setenv("DROMAS_AGENT", socket)
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	encryptIdentity(t, filepath.Join(dir, "identity"), "correct horse")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(askpassEnv, "correct horse")
	var out bytes.Buffer
	if err := do(t.Context(), "prekey renew", []string{"--askpass", exe, "--add-keys-to-agent", "yes"}, nil, &out, io.Discard); err != nil {
		t.Fatalf("a renewal whose agent is not there: %v", err)
	}
	if ls := printed(t, out.String()); typesOf(ls) != "agent_not_added prekey" || ls[0]["socket"] != socket {
		t.Errorf("printed %s", out.String())
	}
}

// No prompt opens once the command's context has ended: nothing would restore
// the terminal it made raw (#803 item 2).
func TestNoPromptOpensOnceTheCommandIsInterrupted(t *testing.T) {
	terminalStdin(t)
	ctx, interrupt := context.WithCancel(t.Context())
	interrupt()
	var errs bytes.Buffer
	e := &cli{ctx: ctx, interrupt: interrupt, cfg: &config{}, stdin: os.Stdin, stderr: &errs}
	if _, err := e.passphrase("identity"); !errors.Is(err, context.Canceled) || errs.Len() != 0 {
		t.Errorf("the prompt after an interrupt: %v; said %q", err, errs.String())
	}
}

// keysRead notes a ^C, which term.Terminal reads as the end of input, so the
// prompt takes it as an interrupt, and a ^D as no passphrase (#803 item 10).
func TestKeysReadNotesACtrlC(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]bool{"pass\r": false, "pa\x03": true, "\x04": false} {
		k := &keysRead{r: strings.NewReader(in)}
		if _, err := io.ReadAll(k); err != nil {
			t.Fatal(err)
		}
		if k.interrupted != want {
			t.Errorf("%q: interrupted %v", in, k.interrupted)
		}
	}
}
