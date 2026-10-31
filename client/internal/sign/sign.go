// Package sign holds a mailbox's keys, and is the one place they sign
// (TestOnlySignSigns). The identity signs whatever the mailbox signs, at any
// time: its prekey statements, passes and messages, the certificates it gives
// working keys, and its revocations. A working key under a certificate the
// identity made signs statements, passes and messages (PROTOCOL.md §5).
//
// Every signature asks the mailbox, at the moment it is made, whether it holds
// its own revocation, and is refused if it does (PROTOCOL.md §12.5): no path, a
// new one included, signs around that check.
package sign

import (
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/protocol"
)

// Finished returns why the mailbox may sign nothing more, or nil.
type Finished func() error

// Identity is a mailbox's identity key.
type Identity struct {
	key      ssh.Signer
	finished Finished
}

// NewIdentity holds key, the mailbox's identity, for a mailbox that says
// whether it is finished.
func NewIdentity(key ssh.Signer, finished Finished) *Identity {
	return &Identity{key: key, finished: finished}
}

// PublicKey is the identity's public key.
func (k *Identity) PublicKey() ssh.PublicKey { return k.key.PublicKey() }

// Certify signs a certificate of the working key work, of serial n, made at
// now and running for lifetime (protocol.NewCertificate).
func (k *Identity) Certify(id *protocol.Identity, work ssh.PublicKey, n uint64, now time.Time, lifetime time.Duration) (*protocol.Certificate, error) {
	if err := k.finished(); err != nil {
		return nil, err
	}
	return protocol.NewCertificate(id, k.key, work, n, now, lifetime)
}

// Revocation signs the revocation of the address a (protocol.SignRevocation).
func (k *Identity) Revocation(a protocol.Address) ([]byte, error) {
	if err := k.finished(); err != nil {
		return nil, err
	}
	return protocol.SignRevocation(k.key, a)
}

// RevokedCertificates signs the revocation of certificates r
// (protocol.SignRevokedCertificates).
func (k *Identity) RevokedCertificates(r *protocol.RevokedCertificates) ([]byte, error) {
	if err := k.finished(); err != nil {
		return nil, err
	}
	return protocol.SignRevokedCertificates(k.key, r)
}

// Statement signs prekey statement p, in its mailbox's prekey namespace.
func (k *Identity) Statement(p *protocol.Prekey) ([]byte, error) {
	return statement(k.key, k.finished, p)
}

// Message signs an envelope's line for its recipient to, in to's message
// namespace.
func (k *Identity) Message(line []byte, to protocol.Address) ([]byte, error) {
	return message(k.key, k.finished, line, to)
}

// Pass signs a pass of the mailbox a, for the server named server, dated now
// (protocol.NewPass).
func (k *Identity) Pass(a protocol.Address, server string, now time.Time) (string, time.Time, error) {
	return pass(k.key, k.finished, a, server, now)
}

// Work is a working key under its certificate: it signs the mailbox's prekey
// statements, passes and messages.
type Work struct {
	key      ssh.Signer
	finished Finished
}

// NewWork holds key, a working key under its certificate, for a mailbox that
// says whether it is finished.
func NewWork(key ssh.Signer, finished Finished) *Work {
	return &Work{key: key, finished: finished}
}

// Statement signs prekey statement p, in its mailbox's prekey namespace.
func (k *Work) Statement(p *protocol.Prekey) ([]byte, error) {
	return statement(k.key, k.finished, p)
}

// Message signs an envelope's line for its recipient to, in to's message
// namespace.
func (k *Work) Message(line []byte, to protocol.Address) ([]byte, error) {
	return message(k.key, k.finished, line, to)
}

// Pass signs a pass of the mailbox a, for the server named server, dated now
// (protocol.NewPass).
func (k *Work) Pass(a protocol.Address, server string, now time.Time) (string, time.Time, error) {
	return pass(k.key, k.finished, a, server, now)
}

func statement(key ssh.Signer, finished Finished, p *protocol.Prekey) ([]byte, error) {
	if err := finished(); err != nil {
		return nil, err
	}
	return protocol.Sign(key, p.Statement(), protocol.PrekeyNamespace(p.Address))
}

func message(key ssh.Signer, finished Finished, line []byte, to protocol.Address) ([]byte, error) {
	if err := finished(); err != nil {
		return nil, err
	}
	return protocol.Sign(key, line, protocol.MessageNamespace(to))
}

func pass(key ssh.Signer, finished Finished, a protocol.Address, server string, now time.Time) (string, time.Time, error) {
	if err := finished(); err != nil {
		return "", time.Time{}, err
	}
	return protocol.NewPass(key, a, server, now)
}
