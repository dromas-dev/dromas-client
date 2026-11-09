//go:build !windows

// The mailbox's own agent is not built for Windows (TestAgentStartAndAddRunStockSSHAgent).

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// Without --askpass, agent add runs ssh-add only where stdin is a terminal and
// no-prompt is not set, and fails as identity_locked otherwise, before
// ssh-add runs or the agent_add line is printed. Where it runs ssh-add, it
// gives it the command's stdin and SSH_ASKPASS_REQUIRE=never, so an askpass
// program the environment names (SSH_ASKPASS, DISPLAY) is not run, and the
// agent_add line names what ssh-add was run with. With --askpass, ssh-add runs
// agent add's counted askpass, forced. The terminal itself is the seam
// isTerminal, since go test gives none: a run under a pseudo-terminal is a
// test by hand.
func TestAgentAddAsksOnlyAtATerminal(t *testing.T) {
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
	t.Setenv("SSH_ASKPASS", "/a/desktop/askpass")
	t.Setenv("DISPLAY", ":0")
	record := fakeSSHAdd(t)
	refused := func(name string, args []string) {
		t.Helper()
		var out bytes.Buffer
		err := do(t.Context(), "agent add", args, strings.NewReader("typed\n"), &out, io.Discard)
		if l := failed(t, err); l["code"] != client.IdentityLocked || l["identity_file"] != filepath.Join(dir, "identity") {
			t.Errorf("%s: %v", name, l)
		}
		if out.Len() != 0 {
			t.Errorf("%s: printed %s", name, out.String())
		}
		if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: ssh-add ran: %v", name, err)
		}
	}
	refused("stdin no terminal", nil)
	terminalStdin(t)
	refused("--no-prompt", []string{"--no-prompt"})
	t.Setenv("DROMAS_NO_PROMPT", "true")
	refused("$DROMAS_NO_PROMPT", nil)
	t.Setenv("DROMAS_NO_PROMPT", "")

	var out bytes.Buffer
	if err := do(t.Context(), "agent add", nil, strings.NewReader("typed\n"), &out, io.Discard); err == nil {
		t.Error("agent add whose ssh-add failed did not fail")
	}
	ran, err := os.ReadFile(record) //nolint:gosec // the test's own
	if err != nil {
		t.Fatalf("ssh-add did not run at a terminal: %v", err)
	}
	want := "SSH_ASKPASS_REQUIRE=never\nSSH_ASKPASS=/a/desktop/askpass\nargs=" + filepath.Join(dir, "identity") + "\ntyped\n"
	if string(ran) != want {
		t.Errorf("ssh-add ran with %q, not %q", ran, want)
	}
	if l := printed(t, out.String()); len(l) != 1 || l[0]["command"] != "SSH_AUTH_SOCK="+socket+" SSH_ASKPASS_REQUIRE=never ssh-add "+filepath.Join(dir, "identity") {
		t.Errorf("agent add's line: %s", out.String())
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := do(t.Context(), "agent add", []string{"--askpass", exe}, nil, io.Discard, io.Discard); err == nil {
		t.Error("agent add whose ssh-add failed did not fail")
	}
	if ran, err := os.ReadFile(record); err != nil || !strings.HasPrefix(string(ran), "SSH_ASKPASS_REQUIRE=force\nSSH_ASKPASS="+exe+"\n") { //nolint:gosec // the test's own
		t.Errorf("ssh-add under --askpass ran with %q: %v", ran, err)
	}
}

// A wrong passphrase from --askpass ends agent add after three tries, as ssh's
// NumberOfPasswordPrompts does, as identity_locked: stock ssh-add asks its
// askpass program again until it answers empty, so agent add counts.
func TestAWrongPassphraseFromAskpassEndsAgentAddAfterThreeTries(t *testing.T) {
	for _, tool := range []string{"ssh-agent", "ssh-add"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
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
	encryptIdentity(t, filepath.Join(dir, "identity"), "correct horse")
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- do(ctx, "agent start", nil, nil, io.Discard, io.Discard) }()
	t.Cleanup(func() { stop(); <-done })
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if c, err := net.Dial("unix", socket); err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no agent answers at %s", socket)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	t.Setenv(askpassEnv, "battery staple")
	t.Setenv(askpassRunsEnv, runs)
	bounded, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	err = do(bounded, "agent add", []string{"--askpass", exe}, nil, io.Discard, io.Discard)
	if l := failed(t, err); l["code"] != client.IdentityLocked {
		t.Errorf("agent add with a wrong passphrase: %v", l)
	}
	if raw, err := os.ReadFile(runs); err != nil || bytes.Count(raw, []byte{'\n'}) != 3 { //nolint:gosec // the test's own
		t.Errorf("the askpass program ran %d times, not 3: %v", bytes.Count(raw, []byte{'\n'}), err)
	}
	// A program that fails gives no passphrase, and agent add is
	// identity_locked as for a wrong one (#803 item 3).
	if fails, err := exec.LookPath("false"); err == nil {
		err = do(bounded, "agent add", []string{"--askpass", fails}, nil, io.Discard, io.Discard)
		if l := failed(t, err); l["code"] != client.IdentityLocked {
			t.Errorf("agent add whose askpass program fails: %v", l)
		}
	}
}

// A key file with no passphrase asks nothing, so agent add runs ssh-add on it
// whatever stdin is; and an ssh-add that fails is said with what it wrote
// (#803 items 4 and 6).
func TestAgentAddOfAKeyWithNoPassphraseRunsSSHAddAndSaysItsFailure(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	t.Setenv("DROMAS_AGENT", filepath.Join(shortDir(t), "s"))
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	record := fakeSSHAdd(t)
	err := do(t.Context(), "agent add", nil, strings.NewReader(""), io.Discard, io.Discard)
	if _, serr := os.Stat(record); serr != nil {
		t.Fatalf("ssh-add did not run on a key file with no passphrase: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "fake ssh-add: refused") {
		t.Errorf("agent add whose ssh-add failed: %v", err)
	}
}

// agent add refuses a revoked mailbox before ssh-add runs: it signs nothing
// more, so its key goes in no agent.
func TestAgentAddRefusesARevokedMailbox(t *testing.T) {
	cleanEnv(t)
	srv := fakeserver.New(t, time.Now)
	dir := t.TempDir()
	t.Setenv("DROMAS_CONFIG", configFor(t, dir))
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	t.Setenv("DROMAS_AGENT", filepath.Join(shortDir(t), "s"))
	if err := do(t.Context(), "init", nil, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, err := client.Open(dir, client.Options{Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := b.Revocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	_, _ = b.Receive(t.Context()) // which keeps the revocation it meets
	if st, err := b.Status(t.Context()); err != nil || !st.Revoked {
		t.Fatalf("the mailbox keeps no revocation of its own: %v", err)
	}
	record := fakeSSHAdd(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = do(t.Context(), "agent add", []string{"--askpass", exe}, nil, &out, io.Discard)
	if gone, ok := errors.AsType[*client.RevokedError](err); !ok || !gone.Own || out.Len() != 0 {
		t.Errorf("agent add of a revoked mailbox: %v; printed %s", err, out.String())
	}
	if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ssh-add ran: %v", err)
	}
}
