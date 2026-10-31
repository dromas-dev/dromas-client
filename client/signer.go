package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// A mailbox's statements, passes and messages are signed by its identity, or
// by a working key under a certificate its identity made (PROTOCOL.md §5);
// every verifier takes the identity's signature, and a certificate above the
// revocation of certificates it holds for the address. A mailbox's statements
// are ordered by (c, p), the identity at rank 0, so once a statement under a
// certificate is taken, an identity statement stands below it and is refused.
//
// This mailbox keeps one signer, the signer of the statement it published
// last: its identity until it has published a statement under a certificate,
// and from then the working key of its current certificate, for its passes
// and its messages too. A renewal is signed under a new certificate once the
// mailbox has published one, where its server serves one, and where its
// owner asks for one (dromas init --offline, dromas cert switch); otherwise
// by the identity.

// signer is what signs a mailbox's statements, passes and messages: its
// identity (sign.Identity), or a working key under its certificate
// (sign.Work).
type signer interface {
	Statement(*protocol.Prekey) ([]byte, error)
	Message(line []byte, to protocol.Address) ([]byte, error)
	Pass(a protocol.Address, server string, now time.Time) (string, time.Time, error)
}

// signerOf is the signer of statement n, as this mailbox signed it: its
// identity, or working key n under its certificate, which it returns too.
func (b *Mailbox) signerOf(n uint64) (signer, *protocol.Certificate, error) {
	rank, err := b.rankOf(n)
	if err != nil {
		return nil, nil, err
	}
	if rank == 0 {
		return b.identity, nil, nil
	}
	w, c, err := b.worker(n)
	if err != nil {
		return nil, nil, err
	}
	return w, c, nil
}

// rankOf is the rank of the signer of statement n, as this mailbox keeps it:
// 0 for its identity, n for working key n (PROTOCOL.md §5).
func (b *Mailbox) rankOf(n uint64) (uint64, error) {
	rec, err := b.prekeyOf(n)
	if err != nil {
		return 0, err
	}
	if rec == nil {
		return 0, fmt.Errorf("the mailbox's state file keeps no statement of prekey %d", n)
	}
	o, err := protocol.PrekeyOrder(rec.Statement, rec.Signature)
	if err != nil {
		return 0, fmt.Errorf("the mailbox's state file keeps a statement of prekey %d that does not parse: %w", n, err)
	}
	return o.Rank, nil
}

// currentSigner is the signer of the statement this mailbox published last.
func (b *Mailbox) currentSigner() (signer, *protocol.Certificate, error) {
	n, err := b.publishedSerial()
	if err != nil {
		return nil, nil, err
	}
	if n == 0 {
		return nil, nil, errors.New("this mailbox has published no prekey: dromas init registers it, and dromas prekey renew recovers one restored from its identity")
	}
	return b.signerOf(n)
}

// certified reports whether the statement this mailbox published last is
// under a certificate: from then on, it signs under one.
func (b *Mailbox) certified() (bool, error) {
	n, err := b.publishedSerial()
	if err != nil || n == 0 {
		return false, err
	}
	rank, err := b.rankOf(n)
	return rank > 0, err
}

// usesRevoked reports whether this mailbox's own revocation of its
// certificates covers the certificate of the statement it published last.
// Such a mailbox takes no messages and signs none until it publishes a
// statement under a certificate above it. Its identity is covered by none.
// One it cannot read covers nothing here: whoever reads it to apply it says
// so.
func (b *Mailbox) usesRevoked() bool {
	n, err := b.publishedSerial()
	if err != nil || n == 0 {
		return false
	}
	rank, err := b.rankOf(n)
	return err == nil && rank > 0 && b.ownRevokes(rank)
}

// noRoom is the refusal of a certificate at a server whose maximum leaves
// it no time to sign messages.
func noRoom(most time.Duration) error {
	return refuse("this mailbox's server takes no certificate longer than %d seconds, which leaves none to sign messages under: a message is signed only under a certificate with more than %d days left, and one begins an hour before it is made, so no certificate is made here; the identity, which needs none, signs instead unless the mailbox has published a statement under a certificate", int64(most/time.Second), protocol.TTL/(24*time.Hour))
}

// certificateLifetime is how long a certificate this mailbox makes runs: the
// longest its server takes, max_certificate_lifetime in its document
// (PROTOCOL.md §11.18), read once a command, and never past
// protocol.LastDate. A maximum that leaves no room to sign messages, the TTL
// and the hour a certificate begins before it is made, refuses the
// certificate (noRoom).
func (b *Mailbox) certificateLifetime(ctx context.Context) (time.Duration, error) {
	if b.lifetime > 0 {
		return b.lifetime, nil
	}
	var d protocol.ServerDocument
	if _, err := b.call(ctx, http.MethodGet, protocol.DocumentPath, nil, nil, &d); err != nil {
		return 0, fmt.Errorf("this mailbox's server's document could not be read, so no certificate is made: a certificate runs for the longest the server takes, which the document names: %w", err)
	}
	life := time.Duration(math.MaxInt64)
	if d.MaxCertificateLifetime < uint64(math.MaxInt64/int64(time.Second)) {
		life = time.Duration(d.MaxCertificateLifetime) * time.Second //nolint:gosec // bounded above
	}
	if life <= protocol.TTL+protocol.MaxSkew {
		return 0, noRoom(life)
	}
	if last := protocol.LastDate.Sub(b.now().Add(-protocol.MaxSkew)); life > last {
		life = last.Truncate(time.Second)
	}
	b.lifetime = life
	return life, nil
}

// RenewUnderCertificate publishes a new prekey under a new working key and
// certificate, as [Mailbox.Rekey] does, whatever signed the mailbox's last
// statement: what dromas cert switch runs. Once a server takes it, an
// identity statement stands below it in the order (c, p) and is refused
// there and at every sender that saw it, so the mailbox signs under
// certificates from then on, for good. Messages its identity signed stay
// good. A server whose maximum leaves a certificate no room refuses it
// before anything is signed (noRoom).
func (b *Mailbox) RenewUnderCertificate(ctx context.Context) (*protocol.Prekey, error) {
	return locked(ctx, b, func() (*protocol.Prekey, error) {
		p, at, err := b.renewAs(ctx, true, true)
		if err != nil {
			return p, err
		}
		if err := b.prune(at); err != nil {
			return p, err
		}
		_, err = b.adoptPending(ctx)
		return p, err
	})
}
