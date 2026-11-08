package client

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/dromas-dev/dromas-client/client/internal/sign"
)

// Where the identity's private key comes from, when a signature needs it
// (sign.Identity): the agent's key that matches the identity, then the key file
// as it is, then the key file and its passphrase. A command lets go of it when
// it ends (locked), but under Options.KeepIdentity.

// IdentityLocked is the code of an [IdentityLockedError].
const IdentityLocked = "identity_locked"

// ErrNoPassphrase is what Options.Passphrase returns where it has no passphrase
// to give: there is no terminal, or prompting is off.
var ErrNoPassphrase = errors.New("no passphrase was given")

// IdentityLockedError is a signature the identity's key was needed for, where
// no agent holds that key and its file cannot be read without a passphrase, or
// is not there. Nothing was signed.
type IdentityLockedError struct {
	// File is the identity key file, and Agent the agent's socket, or empty.
	File, Agent string
	// Reason is why, in this client's words.
	Reason string
}

func (e *IdentityLockedError) Error() string {
	return fmt.Sprintf("the identity's key is locked: %s. dromas agent add loads it into the mailbox's agent; a key file with a passphrase is also unlocked by --askpass, or at a terminal", e.Reason)
}

// Code is the error's code, IdentityLocked.
func (e *IdentityLockedError) Code() string { return IdentityLocked }

// unlocking is where a mailbox has its identity's private key from.
type unlocking struct {
	agent      string
	passphrase func(file string) ([]byte, error)
	keep, add  bool
	lifetime   time.Duration
	notAdded   func(error)
}

// agentPatience is how long the agent may take to answer each request: a
// signature by a hardware key waits for its touch.
var agentPatience = 2 * time.Minute

// identityPublicKey reads the identity's public key: from its key file, as
// ssh-keygen -y does, or, where that file holds it under a passphrase in a form
// that carries no public key, or there is no file because the key is in an
// agent, from identity.pub.
func (b *Mailbox) identityPublicKey() (ssh.PublicKey, error) {
	file := b.path("identity")
	pemBytes, err := os.ReadFile(file) //nolint:gosec // the mailbox's own identity key
	if errors.Is(err, fs.ErrNotExist) {
		if pub, perr := readPublicKey(b.path("identity.pub")); perr == nil {
			return pub, nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("no mailbox here: %w", err)
	}
	if err := checkKeyFile(file); err != nil {
		return nil, err
	}
	raw, err := ssh.ParseRawPrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if missing.PublicKey != nil {
			return missing.PublicKey, nil
		}
		pub, perr := readPublicKey(b.path("identity.pub"))
		if perr != nil {
			return nil, fmt.Errorf("the identity key has a passphrase, and its file names no public key: %s is needed beside it: %w", b.path("identity.pub"), perr)
		}
		return pub, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the identity key: %w", err)
	}
	key, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, fmt.Errorf("the identity key: %w", err)
	}
	return key.PublicKey(), nil
}

// readPublicKey reads a public key file as ssh-keygen writes one beside a key.
func readPublicKey(file string) (ssh.PublicKey, error) {
	raw, err := os.ReadFile(file) //nolint:gosec // the mailbox's own identity.pub
	if err != nil {
		return nil, err
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return pub, nil
}

// unlockIdentity has the identity's private key, for sign.Identity at its first
// signature: the key of it the agent holds, or the key file, unlocked by a
// passphrase where it has one, and added to the agent then if asked. It runs
// under the command's lock, at the signature that needs the key, so a person at
// the prompt holds the lock, and another command of the mailbox waits for it
// as it waits for any command (lockPatience).
func (b *Mailbox) unlockIdentity() (ssh.Signer, func(), error) {
	pub, u := b.id.PublicKey(), b.unlock
	file := b.path("identity")
	locked := func(format string, a ...any) error {
		return &IdentityLockedError{File: file, Agent: u.agent, Reason: fmt.Sprintf(format, a...)}
	}
	agentWhy := "no agent is set"
	if u.agent != "" {
		key, release, err := agentKey(u.agent, pub)
		if err == nil {
			return sign.Lent(key, lockedAt(release, file)), release.close, nil
		}
		agentWhy = err.Error()
	}
	pemBytes, err := os.ReadFile(file) //nolint:gosec // the mailbox's own identity key
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, locked("%s, and there is no key file at %s", agentWhy, file)
	}
	if err != nil {
		return nil, nil, err
	}
	if err := checkKeyFile(file); err != nil {
		return nil, nil, err
	}
	raw, err := ssh.ParseRawPrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if u.passphrase == nil {
			return nil, nil, locked("%s, and the key file %s has a passphrase", agentWhy, file)
		}
		pass, perr := u.passphrase(file)
		if errors.Is(perr, ErrNoPassphrase) {
			return nil, nil, locked("%s, and the key file %s has a passphrase: %v", agentWhy, file, perr)
		}
		if perr != nil {
			return nil, nil, perr
		}
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, pass)
		clear(pass)
		if errors.Is(err, x509.IncorrectPasswordError) {
			return nil, nil, locked("the passphrase given does not unlock the key file %s", file)
		}
		// As ssh's AddKeysToAgent, an agent that does not take the key fails
		// nothing: the command goes on with the key, and is told.
		if err == nil && u.add && u.agent != "" {
			if aerr := addToAgent(u.agent, raw, u.lifetime); aerr != nil && u.notAdded != nil {
				u.notAdded(fmt.Errorf("the identity's key is unlocked, and the agent did not take it: %w", aerr))
			}
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("the identity key: %w", err)
	}
	key, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("the identity key: %w", err)
	}
	return key, nil, nil
}

// forgetIdentity lets go of the identity's private key at a command's end. Under
// Options.KeepIdentity it keeps a key it unlocked from the key file, and lets go
// of an agent's, which the next command has from the agent again, so an agent
// started again since serves it.
func (b *Mailbox) forgetIdentity() {
	if b.identity != nil && (!b.unlock.keep || b.identity.Borrowed()) {
		b.identity.Forget()
	}
}

// agentConn is a connection to an agent, each request on it bounded by
// agentPatience from its write: a request is one write, and its answer read
// after it (agent's client).
type agentConn struct {
	net.Conn
	socket string
	silent bool
}

// errNoAnswer is an agent that did not answer within agentPatience.
var errNoAnswer = errors.New("the agent did not answer")

// dialAgent connects to the agent at socket.
func dialAgent(socket string) (*agentConn, error) {
	conn, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("no agent answers at %s", socket)
	}
	return &agentConn{Conn: conn, socket: socket}, nil
}

func (c *agentConn) Write(p []byte) (int, error) {
	if err := c.SetDeadline(time.Now().Add(agentPatience)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(p)
	c.note(err)
	return n, err
}

func (c *agentConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.note(err)
	return n, err
}

// note keeps that a request ran past agentPatience, which the agent's client
// reports in words of its own alone.
func (c *agentConn) note(err error) {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		c.silent = true
	}
}

// failed is err, a request's failure, as this client says it: errNoAnswer
// where the request ran past agentPatience.
func (c *agentConn) failed(err error) error {
	if c.silent {
		return fmt.Errorf("%w at %s within %s", errNoAnswer, c.socket, agentPatience)
	}
	return err
}

func (c *agentConn) close() { _ = c.Close() }

// agentKey is the key the agent at socket holds of pub, and its connection.
func agentKey(socket string, pub ssh.PublicKey) (ssh.Signer, *agentConn, error) {
	conn, err := dialAgent(socket)
	if err != nil {
		return nil, nil, err
	}
	keys, err := agent.NewClient(conn).Signers()
	if err != nil {
		conn.close()
		if err := conn.failed(err); errors.Is(err, errNoAnswer) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("the agent at %s did not list its keys: %w", socket, err)
	}
	want := pub.Marshal()
	for _, k := range keys {
		if string(k.PublicKey().Marshal()) == string(want) {
			return k, conn, nil
		}
	}
	conn.close()
	return nil, nil, fmt.Errorf("the agent at %s holds no key of this identity", socket)
}

// lockedAt is a signature the agent at conn did not make with the key it
// listed, as an IdentityLockedError: the key is the agent's to give, and one
// that no longer holds it, or does not answer, gives none.
func lockedAt(conn *agentConn, file string) func(error) error {
	return func(err error) error {
		why := fmt.Sprintf("the agent at %s made no signature with the key it listed, as an agent that no longer holds it does not: %v", conn.socket, err)
		if err := conn.failed(err); errors.Is(err, errNoAnswer) {
			why = err.Error()
		}
		return &IdentityLockedError{File: file, Agent: conn.socket, Reason: why}
	}
}

// identityKey is where the identity's private key is, for Status: nothing is
// unlocked, and no passphrase asked.
func (b *Mailbox) identityKey() string {
	if b.unlock.agent != "" && AgentHolds(b.unlock.agent, b.id.PublicKey()) {
		return KeyInAgent
	}
	pemBytes, err := os.ReadFile(b.path("identity")) //nolint:gosec // the mailbox's own identity key
	if err != nil {
		return KeyNone
	}
	var missing *ssh.PassphraseMissingError
	if _, err := ssh.ParseRawPrivateKey(pemBytes); errors.As(err, &missing) {
		return KeyPassphrase
	}
	return KeyFile
}

// AgentHolds reports whether the agent at socket holds the key of the
// identity whose public key is pub.
func AgentHolds(socket string, pub ssh.PublicKey) bool {
	_, conn, err := agentKey(socket, pub)
	if err == nil {
		conn.close()
	}
	return err == nil
}

// addToAgent adds a private key to the agent at socket, for lifetime if not
// zero, as ssh-add does.
func addToAgent(socket string, raw any, lifetime time.Duration) error {
	conn, err := dialAgent(socket)
	if err != nil {
		return err
	}
	defer conn.close()
	return conn.failed(agent.NewClient(conn).Add(agent.AddedKey{PrivateKey: raw, LifetimeSecs: uint32(lifetime / time.Second)})) //nolint:gosec // a lifetime the owner set, in seconds
}
