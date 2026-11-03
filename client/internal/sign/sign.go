// Package sign holds a mailbox's keys, and is the one place they sign
// (TestOnlySignSigns). The identity signs whatever the mailbox signs, at any
// time: its prekey statements, passes and messages, the certificates it gives
// working keys, and its revocations. A working key under a certificate the
// identity made signs statements, passes and messages (PROTOCOL.md §5).
//
// Every signature asks the mailbox, at the moment it is made, whether it holds
// its own revocation, and is refused if it does (PROTOCOL.md §12.5): no path, a
// new one included, signs around that check.
//
// The identity's private key is had only when a signature needs it, after that
// check: from an agent, from its file, or from its file and a passphrase
// (Unlock). It is held until Forget.
package sign

import (
	"bytes"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/protocol"
)

// Finished returns why the mailbox may sign nothing more, or nil.
type Finished func() error

// Unlock gives the identity's private key, and a function that lets go of
// whatever holds it (an agent's connection), or nil.
type Unlock func() (key ssh.Signer, release func(), err error)

// Identity is a mailbox's identity key: its public key, and how its private key
// is had when a signature needs it.
type Identity struct {
	pub      ssh.PublicKey
	unlock   Unlock
	finished Finished

	mu      sync.Mutex
	key     ssh.Signer
	release func()
}

// errOtherKey is a private key Unlock gave that is not the identity's.
var errOtherKey = errors.New("the identity's private key is not the key of its public key: identity.pub and the key file, or the agent, name two keys")

// NewIdentity holds the identity whose public key is pub, its private key had
// from unlock at the first signature, for a mailbox that says whether it is
// finished.
func NewIdentity(pub ssh.PublicKey, unlock Unlock, finished Finished) *Identity {
	return &Identity{pub: pub, unlock: unlock, finished: finished}
}

// PublicKey is the identity's public key.
func (k *Identity) PublicKey() ssh.PublicKey { return k.pub }

// signer is the identity's private key, unlocked at the first call after New or
// Forget, and only after the mailbox says it is not finished, so a finished
// mailbox asks no agent and no passphrase.
func (k *Identity) signer() (ssh.Signer, error) {
	if err := k.finished(); err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.key != nil {
		return k.key, nil
	}
	key, release, err := k.unlock()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(key.PublicKey().Marshal(), k.pub.Marshal()) {
		if release != nil {
			release()
		}
		return nil, errOtherKey
	}
	k.key, k.release = key, release
	return key, nil
}

// Forget lets go of the private key, so the next signature unlocks it again.
func (k *Identity) Forget() {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.release != nil {
		k.release()
	}
	k.key, k.release = nil, nil
}

// Certify signs a certificate of the working key work, of serial n, made at
// now and running for lifetime (protocol.NewCertificate).
func (k *Identity) Certify(id *protocol.Identity, work ssh.PublicKey, n uint64, now time.Time, lifetime time.Duration) (*protocol.Certificate, error) {
	key, err := k.signer()
	if err != nil {
		return nil, err
	}
	return protocol.NewCertificate(id, key, work, n, now, lifetime)
}

// Revocation signs the revocation of the address a (protocol.SignRevocation).
func (k *Identity) Revocation(a protocol.Address) ([]byte, error) {
	key, err := k.signer()
	if err != nil {
		return nil, err
	}
	return protocol.SignRevocation(key, a)
}

// RevokedCertificates signs the revocation of certificates r
// (protocol.SignRevokedCertificates).
func (k *Identity) RevokedCertificates(r *protocol.RevokedCertificates) ([]byte, error) {
	key, err := k.signer()
	if err != nil {
		return nil, err
	}
	return protocol.SignRevokedCertificates(key, r)
}

// Statement signs prekey statement p, in its mailbox's prekey namespace.
func (k *Identity) Statement(p *protocol.Prekey) ([]byte, error) {
	key, err := k.signer()
	if err != nil {
		return nil, err
	}
	return statement(key, k.finished, p)
}

// Message signs an envelope's line for its recipient to, in to's message
// namespace.
func (k *Identity) Message(line []byte, to protocol.Address) ([]byte, error) {
	key, err := k.signer()
	if err != nil {
		return nil, err
	}
	return message(key, k.finished, line, to)
}

// Pass signs a pass of the mailbox a, for the server named server, dated now
// (protocol.NewPass).
func (k *Identity) Pass(a protocol.Address, server string, now time.Time) (string, time.Time, error) {
	key, err := k.signer()
	if err != nil {
		return "", time.Time{}, err
	}
	return pass(key, k.finished, a, server, now)
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
