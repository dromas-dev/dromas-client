package protocol

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/dromas-dev/dromas-client/sshsig"
	"golang.org/x/crypto/ssh"
)

// A mailbox's statements, passes and messages are signed by one of two forms
// of key (PROTOCOL.md §5): the identity key itself, which ranks 0, or a working
// key under a certificate the identity made, which ranks at the certificate's
// serial. A verifier takes the identity's signature always, and a
// certificate above the revocation of certificates it holds for the address,
// every certificate where it holds none. Certificates and revocations are
// signed by the identity, and are never ranked.

// Signer is the key a signature that verified was made by: the identity, or a
// working key under a certificate.
type Signer struct {
	cert *Certificate
}

// Certificate returns the certificate the signature was made under, or nil
// for the identity's own signature.
func (s *Signer) Certificate() *Certificate { return s.cert }

// Identity reports whether the identity made the signature itself.
func (s *Signer) Identity() bool { return s.cert == nil }

// Rank is where the signer stands in its mailbox's order (PROTOCOL.md §5): 0
// for the identity, the certificate's serial for a working key.
func (s *Signer) Rank() uint64 {
	if s.cert == nil {
		return 0
	}
	return s.cert.Serial()
}

// IdentitySigner is the identity's own signer, for a caller that holds an
// identity's signature by other means: its own record.
func IdentitySigner() *Signer { return &Signer{} }

// ErrRevokedSigner is a signature under a certificate the address's
// revocation of certificates covers: one at or below it.
var ErrRevokedSigner = errors.New("signed under a certificate the address's revocation of its certificates covers")

// Admits holds a signer of an address's statement, pass or message to the
// revocation of its certificates its holder keeps, held, nil for none: the
// identity is always taken, and a certificate at or below held is
// ErrRevokedSigner (PROTOCOL.md §5). A revocation of certificates never
// covers the identity: only revoking the address ends it.
func Admits(held *RevokedCertificates, s *Signer) error {
	if s.cert != nil && held != nil && held.Revokes(s.Rank()) {
		return ErrRevokedSigner
	}
	return nil
}

// VerifySigned verifies an armored signature over message, in namespace, by
// the identity id or a working key it certified, under a certificate valid at
// now (CheckCertificate). Its signing key decides which: the identity's key,
// byte for byte, or a certificate. It returns the signer; holding it to the
// revocation of certificates is the caller's (Admits).
func VerifySigned(id *Identity, message, signature []byte, namespace string, now time.Time) (*Signer, error) {
	return verifySigned(id, message, signature, namespace, func(key ssh.PublicKey) (*Certificate, error) { return CheckCertificate(id, key, now) })
}

// verifySigned is VerifySigned with a certificate held to check.
func verifySigned(id *Identity, message, signature []byte, namespace string, check func(ssh.PublicKey) (*Certificate, error)) (*Signer, error) {
	if err := withinBound(signature, id.Type()); err != nil {
		return nil, err
	}
	blob, err := Dearmor(signature)
	if err != nil {
		return nil, err
	}
	key, err := signingKey(blob)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(key.Marshal(), id.PublicKey().Marshal()) {
		if err := sshsig.Verify(id.PublicKey(), message, blob, namespace); err != nil {
			return nil, err
		}
		return &Signer{}, nil
	}
	cert, err := check(key)
	if err != nil {
		return nil, err
	}
	if err := sshsig.Verify(cert.PublicKey(), message, blob, namespace); err != nil {
		return nil, err
	}
	return &Signer{cert: cert}, nil
}

// StatementHash is how a send names the statement it was sealed to
// (PROTOCOL.md §11.10): the SHA-256 of the statement's bytes as served, never
// of its signature, in lowercase hex.
func StatementHash(statement []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(statement))
}
