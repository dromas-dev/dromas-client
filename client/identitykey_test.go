package client_test

import (
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/dromas-dev/dromas-client/client"
)

// testAgent serves an agent of its own on a socket, as ssh-agent does, and
// returns the socket and the agent.
func testAgent(t *testing.T) (string, agent.Agent) {
	t.Helper()
	// A socket's path is short on some systems (104 bytes on macOS), and a test's
	// own directory can be longer.
	dir, err := os.MkdirTemp("", "dra")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	kr := agent.NewKeyring()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = agent.ServeAgent(kr, c)
				_ = c.Close()
			}()
		}
	}()
	return socket, kr
}

// identityPrivate is the private key in a mailbox's identity file.
func identityPrivate(t *testing.T, b *client.Mailbox) any {
	t.Helper()
	raw, err := os.ReadFile(b.Path("identity"))
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.ParseRawPrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// encrypt puts the mailbox's identity file under passphrase, as ssh-keygen -p
// does, in the file itself, so that its permissions stay.
func encrypt(t *testing.T, b *client.Mailbox, passphrase string) {
	t.Helper()
	block, err := ssh.MarshalPrivateKeyWithPassphrase(identityPrivate(t, b), "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(b.Path("identity"), os.O_WRONLY|os.O_TRUNC, 0)
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

// reopen opens the mailbox b's directory again, under opt.
func reopen(t *testing.T, b *client.Mailbox, opt client.Options) *client.Mailbox {
	t.Helper()
	o, err := client.Open(b.Path(), opt)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return o
}

// A mailbox whose key is in its agent, with no key file, signs through the
// agent all it signs with its identity: a renewal, a message and a revocation;
// and no command writes a key file.
func TestAMailboxWhoseKeyIsInItsAgentSignsThroughIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	from, to := e.mailbox(), e.mailbox()
	socket, kr := testAgent(t)
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, from)}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(from.Path("identity")); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Agent = socket
	from = reopen(t, from, opt)
	if st, err := from.Status(t.Context()); err != nil || st.IdentityKey != client.KeyInAgent {
		t.Fatalf("status: %v, %+v", err, st)
	}
	if _, err := from.Rekey(t.Context()); err != nil {
		t.Fatalf("a renewal through the agent: %v", err)
	}
	send(t, from, to, "through the agent")
	if res := receive(t, to); len(res.Delivered) != 1 {
		t.Fatalf("delivered %d", len(res.Delivered))
	}
	if _, err := from.Revocation(t.Context()); err != nil {
		t.Fatalf("a revocation through the agent: %v", err)
	}
	if _, err := os.Stat(from.Path("identity")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a key file was written: %v", err)
	}
}

// Without the agent's key, the same mailbox signs nothing, and says why in its
// own code, identity_locked; status says the key is nowhere.
func TestAMailboxWithNoKeyItCanHaveIsLocked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	before, err := b.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.Path("identity")); err != nil {
		t.Fatal(err)
	}
	// The agent holds a key, of another identity.
	socket, kr := testAgent(t)
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, e.mailbox())}); err != nil {
		t.Fatal(err)
	}
	for name, agentAt := range map[string]string{"no agent": "", "an agent with another key": socket, "no agent at the socket": socket + "x"} {
		opt := e.opts()
		opt.Agent = agentAt
		o := reopen(t, b, opt)
		_, err := o.Rekey(t.Context())
		var locked *client.IdentityLockedError
		if !errors.As(err, &locked) || locked.Code() != client.IdentityLocked {
			t.Errorf("%s: a renewal: %v", name, err)
		}
		st, err := o.Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if st.IdentityKey != client.KeyNone || st.PrekeySerial != before.PrekeySerial || len(st.Attention) == 0 {
			t.Errorf("%s: status after a locked renewal: %+v", name, st)
		}
	}
}

// A key file under a passphrase is unlocked by Options.Passphrase at the first
// signature a command needs, once for that command, and again for the next
// command, but under KeepIdentity; with no passphrase given, or a wrong one,
// it is locked.
func TestAKeyFileUnderAPassphraseIsUnlockedForEachCommand(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	encrypt(t, b, "correct horse")
	asked := 0
	opt := e.opts()
	opt.Passphrase = func(file string) ([]byte, error) {
		asked++
		if file != b.Path("identity") {
			t.Errorf("asked for the passphrase of %s", file)
		}
		return []byte("correct horse"), nil
	}
	o := reopen(t, b, opt)
	if st, err := o.Status(t.Context()); err != nil || st.IdentityKey != client.KeyPassphrase || asked != 0 {
		t.Fatalf("status asked %d times: %v, %+v", asked, err, st)
	}
	for range 2 {
		if _, err := o.Rekey(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 2 {
		t.Errorf("two renewals asked for the passphrase %d times, not once each", asked)
	}
	asked = 0
	opt.KeepIdentity = true
	o = reopen(t, b, opt)
	for range 2 {
		if _, err := o.Rekey(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if asked != 1 {
		t.Errorf("two renewals that keep the identity asked %d times, not once", asked)
	}

	for name, give := range map[string]func(string) ([]byte, error){
		"nothing asked": nil,
		"none given":    func(string) ([]byte, error) { return nil, client.ErrNoPassphrase },
		"a wrong one":   func(string) ([]byte, error) { return []byte("battery staple"), nil },
	} {
		opt := e.opts()
		opt.Passphrase = give
		_, err := reopen(t, b, opt).Rekey(t.Context())
		var locked *client.IdentityLockedError
		if !errors.As(err, &locked) {
			t.Errorf("%s: a renewal: %v", name, err)
		}
	}
}

// Under AddToAgent, a key a passphrase unlocked is added to the agent, for its
// lifetime, so the next command signs through the agent and asks nothing.
func TestAKeyAPassphraseUnlockedIsAddedToTheAgentWhenAsked(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	encrypt(t, b, "correct horse")
	socket, kr := testAgent(t)
	opt := e.opts()
	opt.Agent, opt.AddToAgent, opt.AgentLifetime = socket, true, time.Hour
	opt.Passphrase = func(string) ([]byte, error) { return []byte("correct horse"), nil }
	if _, err := reopen(t, b, opt).Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if keys, err := kr.List(); err != nil || len(keys) != 1 {
		t.Fatalf("the agent holds %d keys: %v", len(keys), err)
	}
	opt.Passphrase = func(string) ([]byte, error) {
		t.Error("asked for a passphrase with the key in the agent")
		return nil, client.ErrNoPassphrase
	}
	o := reopen(t, b, opt)
	if _, err := o.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if st, err := o.Status(t.Context()); err != nil || st.IdentityKey != client.KeyInAgent {
		t.Errorf("status: %v, %+v", err, st)
	}
}

// Init makes a mailbox of identity.pub alone, its key in the agent, and writes
// no key file: a new key would be another address.
func TestInitOfAnIdentityInAnAgentWritesNoKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	made := e.mailbox()
	socket, kr := testAgent(t)
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, made)}); err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(made.Path("identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity.pub"), pub, 0o600); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Agent = socket
	b, err := client.Init(t.Context(), dir, opt)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if b.Address() != made.Address() {
		t.Errorf("init made %s, not the agent's identity %s", b.Address(), made.Address())
	}
	if _, err := os.Stat(filepath.Join(dir, "identity")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("init wrote a key file: %v", err)
	}
}
