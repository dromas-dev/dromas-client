package client_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
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
	// Under KeepIdentity too, each command has the key from the agent again: once
	// the agent drops it, the next command is locked.
	opt.KeepIdentity = true
	from = reopen(t, from, opt)
	if _, err := from.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := kr.RemoveAll(); err != nil {
		t.Fatal(err)
	}
	var locked *client.IdentityLockedError
	if _, err := from.Rekey(t.Context()); !errors.As(err, &locked) {
		t.Errorf("a renewal after the agent dropped the key: %v", err)
	}
}

// Without the agent's key, the same mailbox signs nothing, and says why in its
// own code, identity_locked; status says the key is nowhere, and needs a look
// only once a renewal is due.
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
		if st.IdentityKey != client.KeyNone || st.PrekeySerial != before.PrekeySerial || len(st.Attention) != 0 {
			t.Errorf("%s: status after a locked renewal: %+v", name, st)
		}
	}
	e.clock.Add(25 * time.Hour)
	st, err := reopen(t, b, e.opts()).Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Attention) != 1 || !strings.Contains(st.Attention[0], "due for renewal") {
		t.Errorf("status of a locked mailbox due for renewal: %q", st.Attention)
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

// A renewal that failed as identity_locked is not held to renewRetry, as one
// a server refused is: the next Receive tries it again, and says it failed as
// identity_locked again while the key is locked, and renews once it has the
// key, a minute later.
func TestARenewalTheKeyWasLockedForIsTriedAtTheNextReceive(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b := e.mailbox()
	encrypt(t, b, "correct horse")
	before := numberOf(t, b)
	e.clock.Add(25 * time.Hour)
	opt := e.opts()
	opt.Passphrase = func(string) ([]byte, error) { return nil, client.ErrNoPassphrase }
	for i := range 2 {
		res, err := reopen(t, b, opt).Receive(t.Context())
		var locked *client.IdentityLockedError
		if err != nil || !errors.As(res.RenewErr, &locked) {
			t.Fatalf("receive %d with the key locked: %v; its renewal: %v", i+1, err, res.RenewErr)
		}
		e.clock.Add(time.Minute)
	}
	opt.Passphrase = func(string) ([]byte, error) { return []byte("correct horse"), nil }
	res, err := reopen(t, b, opt).Receive(t.Context())
	if err != nil || res.RenewErr != nil || numberOf(t, b) == before {
		t.Errorf("receive with the key, a minute after a renewal it was locked for: %v; its renewal: %v; serial %d, %d before", err, res.RenewErr, numberOf(t, b), before)
	}
}

// serveAgent serves a on a socket of its own, as ssh-agent does, and returns
// the socket.
func serveAgent(t *testing.T, a agent.Agent) string {
	t.Helper()
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
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = agent.ServeAgent(a, c)
				_ = c.Close()
			}()
		}
	}()
	return socket
}

// signsNot is an agent that lists its keys and, before it signs, runs before:
// one that drops the key, or one that stops answering.
type signsNot struct {
	agent.Agent
	before func()
}

func (a signsNot) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	a.before()
	return a.Agent.Sign(key, data)
}

// An agent that lists the identity's key and drops it before it signs, as one
// whose key's lifetime ends between the two, leaves the identity locked: the
// signature fails as identity_locked, and nothing is signed. A key the agent
// lists is the key used, so the key file is not tried, one with no passphrase
// included (#803 item 7).
func TestAnAgentThatDropsTheKeyBeforeItSignsIsLocked(t *testing.T) {
	t.Parallel()
	for _, passphrase := range []string{"correct horse", ""} {
		e := newEnv(t)
		b := e.mailbox()
		before := numberOf(t, b)
		kr := agent.NewKeyring()
		if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, b)}); err != nil {
			t.Fatal(err)
		}
		if passphrase != "" {
			encrypt(t, b, passphrase)
		}
		opt := e.opts()
		opt.Agent = serveAgent(t, signsNot{kr, func() { _ = kr.RemoveAll() }})
		_, err := reopen(t, b, opt).Rekey(t.Context())
		var locked *client.IdentityLockedError
		if !errors.As(err, &locked) || locked.Agent != opt.Agent {
			t.Errorf("a renewal whose agent dropped the key it listed, passphrase %q: %v", passphrase, err)
		}
		if numberOf(t, b) != before {
			t.Errorf("a renewal with the key dropped, passphrase %q, renewed: %d, %d before", passphrase, numberOf(t, b), before)
		}
	}
}

// An agent that does not answer within its patience, a hardware key's touch
// long, leaves the identity locked, with words that say the agent did not
// answer: one that lists nothing, as a stopped ssh-agent, and one that stops
// answering before it signs.
func TestAnAgentThatDoesNotAnswerIsLockedPastItsPatience(t *testing.T) {
	e := newEnv(t)
	b := e.mailbox()
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, b)}); err != nil {
		t.Fatal(err)
	}
	encrypt(t, b, "correct horse")
	client.SetAgentPatience(t, 200*time.Millisecond)
	stuck := make(chan struct{})
	defer close(stuck)
	silent := serveAgent(t, signsNot{kr, func() { <-stuck }})
	listsNothing := filepath.Join(filepath.Dir(silent), "q")
	l, err := net.Listen("unix", listsNothing)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() }) // accepted, and never answered
		}
	}()
	for name, socket := range map[string]string{"an agent that lists nothing": listsNothing, "an agent that does not sign": silent} {
		opt := e.opts()
		opt.Agent = socket
		done := make(chan error, 1)
		go func() { _, err := reopen(t, b, opt).Rekey(t.Context()); done <- err }()
		select {
		case err := <-done:
			var locked *client.IdentityLockedError
			if !errors.As(err, &locked) || !strings.Contains(locked.Reason, "did not answer") {
				t.Errorf("%s: %v", name, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s: a renewal still waits on its agent", name)
		}
	}
}

// Under AddToAgent, an agent that does not take the key a passphrase unlocked
// fails nothing, as ssh's AddKeysToAgent: the command signs with the key, and
// NotAdded is told.
func TestAnAgentThatDoesNotTakeTheKeyFailsNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	before := numberOf(t, b)
	encrypt(t, b, "correct horse")
	var told []error
	opt := e.opts()
	opt.Agent, opt.AddToAgent = filepath.Join(t.TempDir(), "none"), true
	opt.Passphrase = func(string) ([]byte, error) { return []byte("correct horse"), nil }
	opt.NotAdded = func(err error) { told = append(told, err) }
	if _, err := reopen(t, b, opt).Rekey(t.Context()); err != nil {
		t.Errorf("a renewal whose agent is not there: %v", err)
	}
	if len(told) != 1 || numberOf(t, b) == before {
		t.Errorf("told %v; serial %d, %d before", told, numberOf(t, b), before)
	}
}

// explicitCurveKey is a P-256 key in PEM, under passphrase where it is not
// empty, whose curve is given by explicit parameters, as ssh-keygen -m PEM on
// LibreSSL writes one (RFC 5480 §2.1.1's specifiedCurve, SEC 1 §C.2): Go's
// x509 refuses it on purpose. It returns the PEM and the key's public key.
func explicitCurveKey(t *testing.T, passphrase string) ([]byte, ssh.PublicKey) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	d, err := priv.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	point, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	c := elliptic.P256().Params()
	a := new(big.Int).Sub(c.P, big.NewInt(3))
	type fieldID struct {
		Type  asn1.ObjectIdentifier
		Prime *big.Int
	}
	type curve struct{ A, B []byte }
	type ecParameters struct {
		Version  int
		Field    fieldID
		Curve    curve
		Base     []byte
		Order    *big.Int
		Cofactor int
	}
	type ecPrivateKey struct {
		Version    int
		PrivateKey []byte
		Parameters ecParameters   `asn1:"explicit,tag:0"`
		PublicKey  asn1.BitString `asn1:"explicit,tag:1"`
	}
	der, err := asn1.Marshal(ecPrivateKey{
		Version:    1,
		PrivateKey: d,
		Parameters: ecParameters{
			Version:  1,
			Field:    fieldID{asn1.ObjectIdentifier{1, 2, 840, 10045, 1, 1}, c.P}, // prime-field
			Curve:    curve{a.FillBytes(make([]byte, 32)), c.B.FillBytes(make([]byte, 32))},
			Base:     elliptic.Marshal(elliptic.P256(), c.Gx, c.Gy), //nolint:staticcheck // the generator's encoding, as a specifiedCurve writes it
			Order:    c.N,
			Cofactor: 1,
		},
		PublicKey: asn1.BitString{Bytes: point, BitLength: 8 * len(point)},
	})
	if err != nil {
		t.Fatal(err)
	}
	block := &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}
	if passphrase != "" {
		block, err = x509.EncryptPEMBlock(rand.Reader, block.Type, der, []byte(passphrase), x509.PEMCipherAES128) //nolint:staticcheck // ssh-keygen -m PEM's own legacy encryption
		if err != nil {
			t.Fatal(err)
		}
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), pub
}

// A PEM EC key whose curve is given by explicit parameters, which Go's x509
// refuses on purpose, is refused with words that name it and its fix, the
// OpenSSH format: without a passphrase, and under one, given.
func TestAKeyWhoseCurveIsExplicitIsRefusedWithItsFix(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for name, passphrase := range map[string]string{"no passphrase": "", "a passphrase": "correct horse"} {
		key, pub := explicitCurveKey(t, passphrase)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "identity"), key, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "identity.pub"), ssh.MarshalAuthorizedKey(pub), 0o600); err != nil {
			t.Fatal(err)
		}
		opt := e.opts()
		opt.Passphrase = func(string) ([]byte, error) { return []byte(passphrase), nil }
		_, err := client.Init(t.Context(), dir, opt)
		if err == nil || !strings.Contains(err.Error(), "explicit parameters") || !strings.Contains(err.Error(), "ssh-keygen -p -f "+filepath.Join(dir, "identity")) || !strings.Contains(err.Error(), "OpenSSH format") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// lists counts the lists an agent answers: one at each command that has the
// identity's key from it.
type lists struct {
	agent.Agent
	n *atomic.Int32
}

func (a lists) List() ([]*agent.Key, error) {
	a.n.Add(1)
	return a.Agent.List()
}

// Revocation, a reading that signs, lets go of the identity's key at its end,
// as every command does: without KeepIdentity, the next command asks for the
// passphrase again, and with the key from the agent, under KeepIdentity too,
// the next command has it from the agent again.
func TestARevocationLetsGoOfTheIdentitysKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, b)}); err != nil {
		t.Fatal(err)
	}
	encrypt(t, b, "correct horse")
	asked := 0
	opt := e.opts()
	opt.Passphrase = func(string) ([]byte, error) { asked++; return []byte("correct horse"), nil }
	o := reopen(t, b, opt)
	if _, err := o.Revocation(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if asked != 2 {
		t.Errorf("Revocation then Rekey asked for the passphrase %d times, not once each", asked)
	}

	var listed atomic.Int32
	opt = e.opts()
	opt.Agent, opt.KeepIdentity = serveAgent(t, lists{kr, &listed}), true
	o = reopen(t, b, opt)
	if _, err := o.Revocation(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if listed.Load() != 2 {
		t.Errorf("Revocation then Rekey had the key from the agent %d times, not once each", listed.Load())
	}
}

// A revoked mailbox reaches no agent: status of one makes no connection to the
// agent's socket, and says where its key file is alone.
func TestStatusOfARevokedMailboxAsksNoAgent(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	revoke(t, e, b)
	if _, err := b.Receive(t.Context()); err == nil {
		t.Fatal("a revoked mailbox received")
	}
	var dialed atomic.Int32
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
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			_ = c.Close()
		}
	}()
	opt := e.opts()
	opt.Agent = socket
	st, err := reopen(t, b, opt).Status(t.Context())
	if err != nil || !st.Revoked || st.IdentityKey != client.KeyFile {
		t.Fatalf("status of a revoked mailbox: %v, %+v", err, st)
	}
	if dialed.Load() != 0 {
		t.Errorf("status of a revoked mailbox reached its agent %d times", dialed.Load())
	}
}

// init takes an identity.pub it cannot read for an identity it cannot read,
// never for no identity: a link to nothing (a volume not mounted), an empty
// file and one it may not read are refused, and init writes no key and leaves
// identity.pub as it was.
func TestInitRefusesAnIdentityPubItCannotRead(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for name, plant := range map[string]func(pub string) error{
		"a link to nothing": func(pub string) error {
			return os.Symlink(filepath.Join(filepath.Dir(pub), "unmounted", "identity.pub"), pub)
		},
		"an empty file": func(pub string) error { return os.WriteFile(pub, nil, 0o600) },
		"a file it may not read": func(pub string) error {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				return errSkip
			}
			return os.WriteFile(pub, []byte("ssh-ed25519 AAAA\n"), 0o200)
		},
	} {
		dir := t.TempDir()
		pub := filepath.Join(dir, "identity.pub")
		if err := plant(pub); errors.Is(err, errSkip) {
			continue
		} else if err != nil {
			t.Logf("%s: not made here: %v", name, err)
			continue
		}
		before, err := os.Lstat(pub)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Init(t.Context(), dir, e.opts()); err == nil {
			t.Errorf("%s: init made a mailbox", name)
		}
		if _, err := os.Lstat(filepath.Join(dir, "identity")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: init wrote a key: %v", name, err)
		}
		if after, err := os.Lstat(pub); err != nil || after.Mode() != before.Mode() || after.Size() != before.Size() {
			t.Errorf("%s: identity.pub after init: %v, %v", name, after, err)
		}
	}
}

// errSkip is a case a system cannot make.
var errSkip = errors.New("not on this system")

// The identity's public key is the key file's where one exists, and
// identity.pub's otherwise: an identity.pub that names another key changes
// nothing beside a key file, and an agent that holds the key identity.pub
// names, and not the key file's, gives no key.
func TestTheIdentitysPublicKeyIsTheKeyFilesWhereOneExists(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b, other := e.mailbox(), e.mailbox()
	pub, err := os.ReadFile(other.Path("identity.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.Path("identity.pub"), pub, 0o600); err != nil {
		t.Fatal(err)
	}
	o := reopen(t, b, e.opts())
	if o.Address() != b.Address() {
		t.Fatalf("the mailbox's address is %s, not its key file's %s", o.Address(), b.Address())
	}
	if _, err := o.Rekey(t.Context()); err != nil {
		t.Errorf("a renewal beside an identity.pub of another key: %v", err)
	}
	encrypt(t, b, "correct horse")
	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{PrivateKey: identityPrivate(t, other)}); err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.Agent = serveAgent(t, kr)
	var locked *client.IdentityLockedError
	if _, err := reopen(t, b, opt).Rekey(t.Context()); !errors.As(err, &locked) {
		t.Errorf("a renewal whose agent holds identity.pub's key and not the key file's: %v", err)
	}
}

// Init beside an identity that is a link to nothing, as on a volume not
// mounted, makes no new identity: with the key in the agent it runs as the
// mailbox identity.pub names, and with none it fails as identity_locked, the
// link left as it was (#803 item 8).
func TestInitBesideAnIdentityThatIsALinkToNothingMakesNoNewIdentity(t *testing.T) {
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
	for _, withAgent := range []bool{true, false} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "identity.pub"), pub, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "identity")
		if err := os.Symlink(filepath.Join(dir, "unmounted", "identity"), link); err != nil {
			t.Logf("a link to nothing: not made here: %v", err) // Windows without the right to make a symlink
			return
		}
		opt := e.opts()
		if withAgent {
			opt.Agent = socket
		}
		b, err := client.Init(t.Context(), dir, opt)
		var locked *client.IdentityLockedError
		switch {
		case withAgent && (err != nil || b.Address() != made.Address()):
			t.Errorf("init with the key in the agent: %v", err)
		case !withAgent && !errors.As(err, &locked):
			t.Errorf("init with no agent: %v", err)
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link was replaced, agent %v: %v", withAgent, err)
		}
	}
}

// A renewal that a ^C at the passphrase's prompt cut short is held to no hour:
// the next Receive, a minute later, asks again and renews (#803 item 5).
func TestARenewalInterruptedAtThePromptIsNotHeld(t *testing.T) {
	e := newWorkEnv(t)
	b := e.mailbox()
	encrypt(t, b, "correct horse")
	before := numberOf(t, b)
	e.clock.Add(25 * time.Hour)
	opt := e.opts()
	ctx, interrupt := context.WithCancel(t.Context())
	opt.Passphrase = func(string) ([]byte, error) { interrupt(); return nil, ctx.Err() }
	_, _ = reopen(t, b, opt).Receive(ctx)
	e.clock.Add(time.Minute)
	asked := 0
	opt.Passphrase = func(string) ([]byte, error) { asked++; return []byte("correct horse"), nil }
	res, err := reopen(t, b, opt).Receive(t.Context())
	if err != nil || asked == 0 || res.RenewErr != nil || numberOf(t, b) == before {
		t.Errorf("the Receive after an interrupted renewal: asked %d, renewal %v, %v", asked, res.RenewErr, err)
	}
}
