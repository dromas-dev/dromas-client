// Package sktest is a security key in software, for tests: an [ssh.Signer]
// that signs as a FIDO authenticator does for OpenSSH's security-key types,
// sk-ecdsa-sha2-nistp256@openssh.com and sk-ssh-ed25519@openssh.com
// (OpenSSH's PROTOCOL.u2f). golang.org/x/crypto/ssh verifies both types and
// makes neither, and no stock tool signs as one without a hardware token or
// a provider built from source, so the tests of a client or a server that
// takes a mailbox of either type sign with this.
//
// It is for tests alone. Its private key is an ordinary key in memory, and
// it claims user presence, or any flags it is given, without anyone present:
// what a real authenticator attests to by a touch, this asserts by a field.
// Nothing a verifier reads tells the two apart (PROTOCOL.u2f carries no
// attestation in a key or a signature), which is why only a test may use it.
//
// What it signs is PROTOCOL.u2f's: the SHA-256 of the application, the flags,
// the counter, then the SHA-256 of the data. An Ed25519 key signs those bytes;
// a P-256 key signs their SHA-256, deterministically (RFC 6979), so the same
// key, flags, counter and data give the same signature every time.
package sktest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"io"
	"math/big"

	"golang.org/x/crypto/ssh"
)

// Application is the application every key here is made for, as ssh-keygen
// enrolls a security key by default.
const Application = "ssh:"

// UserPresent is the flag of a signature made with a user present (PROTOCOL.u2f;
// the UP bit of WebAuthn's authenticator data).
const UserPresent = 0x01

// Signer is a security key in software. Flags and Counter are what each
// signature it makes carries; New sets Flags to UserPresent.
type Signer struct {
	Flags   byte
	Counter uint32

	application string
	key         crypto.Signer
	pub         ssh.PublicKey
}

// New returns a security key for the private key priv, an ed25519.PrivateKey
// or a P-256 *ecdsa.PrivateKey, made for application: an
// sk-ssh-ed25519@openssh.com or an sk-ecdsa-sha2-nistp256@openssh.com key.
func New(priv crypto.Signer, application string) (*Signer, error) {
	var wire []byte
	switch k := priv.(type) {
	case ed25519.PrivateKey:
		wire = ssh.Marshal(struct {
			Type, Key, Application string
		}{ssh.KeyAlgoSKED25519, string(k.Public().(ed25519.PublicKey)), application})
	case *ecdsa.PrivateKey:
		if k.Curve != elliptic.P256() {
			return nil, errors.New("sktest: an ECDSA security key is a P-256 key")
		}
		point, err := k.PublicKey.Bytes()
		if err != nil {
			return nil, err
		}
		wire = ssh.Marshal(struct {
			Type, Curve, Key, Application string
		}{ssh.KeyAlgoSKECDSA256, "nistp256", string(point), application})
	default:
		return nil, errors.New("sktest: a security key is an Ed25519 or a P-256 key")
	}
	pub, err := ssh.ParsePublicKey(wire)
	if err != nil {
		return nil, err
	}
	return &Signer{Flags: UserPresent, application: application, key: priv, pub: pub}, nil
}

// PublicKey returns the key's public half, of its security-key type.
func (s *Signer) PublicKey() ssh.PublicKey { return s.pub }

// Sign signs data as an authenticator does, with s.Flags and s.Counter. It
// reads nothing from rand: both key types sign deterministically.
func (s *Signer) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	app, msg := sha256.Sum256([]byte(s.application)), sha256.Sum256(data)
	tail := binary.BigEndian.AppendUint32([]byte{s.Flags}, s.Counter)
	signed := append(append(app[:], tail...), msg[:]...)
	var blob []byte
	switch k := s.key.(type) {
	case ed25519.PrivateKey:
		blob = ed25519.Sign(k, signed)
	case *ecdsa.PrivateKey:
		h := sha256.Sum256(signed)
		der, err := k.Sign(nil, h[:], crypto.SHA256) // a nil rand: RFC 6979
		if err != nil {
			return nil, err
		}
		var rs struct{ R, S *big.Int }
		if rest, err := asn1.Unmarshal(der, &rs); err != nil || len(rest) != 0 {
			return nil, errors.New("sktest: crypto/ecdsa wrote no signature")
		}
		blob = ssh.Marshal(rs)
	}
	return &ssh.Signature{Format: s.pub.Type(), Blob: blob, Rest: tail}, nil
}
