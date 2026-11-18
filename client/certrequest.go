package client

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/crypto/ssh"
)

// A mailbox whose identity is kept elsewhere signs under certificates that
// its identity holder makes (PROTOCOL.md §12.7). The mailbox makes each
// working key, and the holder certifies only the working key's public half,
// with ssh-keygen -s: RequestCertificate makes a NEW working key, kept as
//
//	work/request        the working key, OpenSSH format
//	work/request.pub    its public key
//
// with the request's lowest serial in the mailbox's state, and says what the
// holder certifies; InstallCertificate checks the certificate the holder gives
// back by every rule of §12.7 step 3, keeps it as work/<serial>, and renews
// under it. Between certificates, the mailbox renews under the newest one it
// holds (heldWorker).

// keyCertificateRequest is the record, in the mailbox's own bucket, of the
// certificate request the mailbox made last.
const keyCertificateRequest = "certificate-request"

// certificateRequest is what the state file keeps of a certificate request:
// the lowest serial it gave, and the highest serial the mailbox's identity
// had revoked when it was made.
type certificateRequest struct {
	Lowest         uint64 `json:"lowest"`
	RevokedThrough uint64 `json:"revoked_through"`
}

// A CertificateRequest is what a mailbox asks its identity holder to certify
// (PROTOCOL.md §12.7 step 1).
type CertificateRequest struct {
	// Address is the mailbox's address, the certificate's one principal.
	Address protocol.Address `json:"address"`
	// WorkingKey is the public half of the request's new working key, as
	// ssh-keygen writes a .pub file: the key the certificate certifies.
	WorkingKey string `json:"working_key"`
	// LowestSerial is the lowest certificate serial the mailbox accepts: one
	// above the highest certificate serial it knows of for itself
	// (PROTOCOL.md §5.2).
	LowestSerial uint64 `json:"lowest_serial"`
	// Lifetime is the longest the certificate may run, valid before less
	// valid after, in seconds: the least of the protocol's maximum, the
	// server's and the owner's certificate lifetime.
	Lifetime int64 `json:"max_certificate_lifetime_seconds"`
	// Command is the ssh-keygen command that makes the certificate, run where
	// the identity is, with the working key saved as request.pub and the
	// identity key file named identity: it writes request-cert.pub. Its serial
	// is this machine's clock, or the lowest serial where that is higher.
	Command string `json:"command"`
}

// RequestCertificate makes a NEW working key for the mailbox's identity
// holder to certify, and returns the request: what [Mailbox.InstallCertificate]
// then takes. Each request makes a new working key, and replaces the one
// before. It is for a mailbox under certificates: one whose identity signs
// is refused, since it moves to certificates only through dromas cert switch,
// with its identity.
//
// It reads the mailbox at its server first, keeping what verifies of its own
// revocations, so the lowest serial is above every certificate serial the
// mailbox knows of: the one it signs under, the one its server serves, every
// certificate it holds, and the highest its identity has revoked (PROTOCOL.md
// §5.2). A read it cannot use stops nothing: the serial is above what the
// mailbox knows.
func (b *Mailbox) RequestCertificate(ctx context.Context) (*CertificateRequest, error) {
	return locked(ctx, b, func() (*CertificateRequest, error) {
		if err := b.underCertificates(); err != nil {
			return nil, err
		}
		st, err := b.readOwn(ctx, true, true)
		if err != nil {
			return nil, err
		}
		life, err := b.certificateLifetime(ctx)
		if err != nil {
			return nil, err
		}
		highest, revoked, err := b.highestCertificate(st)
		if err != nil {
			return nil, err
		}
		if err := noneAbove(highest); err != nil {
			return nil, err
		}
		priv, err := newKey(protocol.WorkingKeyType(b.id.Type()))
		if err != nil {
			return nil, err
		}
		block, err := ssh.MarshalPrivateKey(priv, "")
		if err != nil {
			return nil, err
		}
		pub, err := ssh.NewPublicKey(priv.Public())
		if err != nil {
			return nil, err
		}
		if err := b.finished(); err != nil {
			return nil, err
		}
		if err := makeDir(b.path("work")); err != nil {
			return nil, err
		}
		stem := b.path("work", "request")
		if err := writeKey(stem, pem.EncodeToMemory(block)); err != nil {
			return nil, err
		}
		line := ssh.MarshalAuthorizedKey(pub)
		if err := writeFile(stem+".pub", line); err != nil {
			return nil, err
		}
		step("request key made")
		rec, err := json.Marshal(certificateRequest{Lowest: highest + 1, RevokedThrough: revoked})
		if err != nil {
			return nil, err
		}
		if err := b.update(func(s store) error { return s.put(bucketMailbox, keyCertificateRequest, rec) }); err != nil {
			return nil, err
		}
		step("request recorded")
		serial := protocol.SerialAt(b.now(), highest)
		return &CertificateRequest{
			Address:      b.id.Address(),
			WorkingKey:   strings.TrimSpace(string(line)),
			LowestSerial: highest + 1,
			Lifetime:     int64(life / time.Second),
			Command:      fmt.Sprintf("ssh-keygen -s identity -I %s -n %s -z %d -V -1h:+%ds -O clear request.pub", protocol.CertificateKeyID, b.id.Address(), serial, int64((life-protocol.MaxSkew)/time.Second)),
		}, nil
	})
}

// underCertificates is the refusal of a certificate request, or of a
// certificate to install, on a mailbox whose identity signs.
func (b *Mailbox) underCertificates() error {
	certified, err := b.certified()
	if err != nil {
		return err
	}
	if !certified {
		return refuse("this mailbox has published no prekey statement under a certificate, so it asks for no certificate: dromas init registers or restores it, and dromas cert switch moves one its identity signs to certificates, each run where the identity's key is")
	}
	return nil
}

// highestCertificate is the highest certificate serial this mailbox knows of
// for itself (PROTOCOL.md §5.2), from st, its read of itself: the
// certificate of the statement it published last, the one its server serves,
// each certificate it holds, the highest number a renewal has taken, which a
// working key made for it would be numbered by, and the highest serial its
// identity has revoked, which it returns too.
func (b *Mailbox) highestCertificate(st *ownRead) (highest, revoked uint64, err error) {
	if highest, err = b.publishedRank(); err != nil {
		return 0, 0, err
	}
	if st.served != nil {
		highest = max(highest, st.served.Rank())
	}
	held, err := b.heldCertificates()
	if err != nil {
		return 0, 0, err
	}
	if len(held) > 0 {
		highest = max(highest, held[len(held)-1])
	}
	begun, err := b.highestBegun()
	if err != nil {
		return 0, 0, err
	}
	highest = max(highest, begun)
	own, err := b.revokedCerts(b.id.Address())
	if err != nil {
		return 0, 0, err
	}
	if own != nil {
		revoked = own.Through
	}
	return max(highest, revoked), revoked, nil
}

// InstallCertificate takes the certificate the mailbox's identity holder made
// for its request, line as ssh-keygen writes a -cert.pub file, and renews
// under it, as the mailbox's owner asking (PROTOCOL.md §12.4). It reads the
// mailbox at its server first, keeping what verifies of its own revocations,
// and then checks the certificate by every rule of §12.7 step 3 before
// anything is kept: every rule of §5.2 at this machine's clock, a lifetime no
// longer than its server takes (serverCertificateLifetime), a serial no lower
// than the request's lowest, and the request's working key. It is refused,
// too, where it began less than an hour before the clock (§12.4), and where
// the identity has revoked certificates above what the request knew of, kept
// at this mailbox or taken by its server from anywhere: the request's working
// key may have been in the mailbox then, so it may be stolen with the rest
// (§12.7, the remedy's step 2), and a new request is made. A revocation of
// this certificate is among those, since the request's lowest serial is above
// every revocation the mailbox kept when it made it. A request made while the
// mailbox's read of itself went unanswered knew only what the mailbox kept,
// so a revocation its server held then refuses the certificate too: the cost
// is a new request.
//
// Where the read or the server's document goes unanswered, nothing is kept,
// and the install is run again. Once checked, the certificate and its working
// key are kept as work/<serial> first, and the request goes once the renewal
// is taken: run again, it does the same, and recv's next renewal is under
// that certificate too (heldWorker).
func (b *Mailbox) InstallCertificate(ctx context.Context, line []byte) (*protocol.Prekey, error) {
	return locked(ctx, b, func() (*protocol.Prekey, error) {
		if err := b.keysMatch(); err != nil {
			return nil, err
		}
		if err := b.underCertificates(); err != nil {
			return nil, err
		}
		// The read first: it keeps every revocation of the mailbox's own that
		// its server holds, so the check below meets one its identity holder
		// published from anywhere (PROTOCOL.md §12.7, the remedy's step 2).
		// Where it cannot be made, nothing is kept, and the install is run
		// again once the server answers.
		st, err := b.readOwn(ctx, false, false)
		if err != nil {
			return nil, fmt.Errorf("the certificate is not checked or kept, since this mailbox could not read itself at its server: dromas cert install runs again with the same file once it can: %w", err)
		}
		life, err := b.serverCertificateLifetime(ctx)
		if err != nil {
			return nil, fmt.Errorf("the certificate is not checked or kept, since this mailbox's server's document, which names the longest certificate it takes, could not be read: dromas cert install runs again with the same file once it can: %w", err)
		}
		cert, err := b.checkInstalled(line, life)
		if err != nil {
			return nil, err
		}
		stem, n := b.path("work", "request"), cert.Serial()
		pemBytes, err := os.ReadFile(stem) //nolint:gosec // a path inside the mailbox's own directory
		if err != nil {
			return nil, fmt.Errorf("the request's working key: %w", err)
		}
		if err := writeKey(b.workStem(n), pemBytes); err != nil {
			return nil, err
		}
		if err := writeFile(b.workStem(n)+".pub", ssh.MarshalAuthorizedKey(cert.WorkingKey())); err != nil {
			return nil, err
		}
		// The certificate last: a working key with no certificate is one that
		// signed nothing.
		if err := writeFile(b.workStem(n)+"-cert.pub", ssh.MarshalAuthorizedKey(cert.PublicKey())); err != nil {
			return nil, err
		}
		step("certificate installed")
		st.certify, st.under = true, n
		p, at, err := b.renewFrom(ctx, st)
		if err != nil {
			return p, err
		}
		if err := b.update(func(s store) error { return s.del(bucketMailbox, keyCertificateRequest) }); err != nil {
			return p, err
		}
		for _, name := range []string{stem, stem + ".pub"} {
			if err := removeIfExists(name); err != nil {
				return p, err
			}
		}
		return p, b.prune(at)
	})
}

// checkInstalled holds line, a certificate the identity holder made, to the
// request this mailbox made last and to life, the longest its server takes
// (serverCertificateLifetime), and returns it (InstallCertificate).
func (b *Mailbox) checkInstalled(line []byte, life time.Duration) (*protocol.Certificate, error) {
	var req certificateRequest
	if err := b.view(func(s store) error {
		raw := s.get(bucketMailbox, keyCertificateRequest)
		if raw == nil {
			return refuse("this mailbox has asked for no certificate: dromas cert request makes the request its identity holder certifies")
		}
		return protocol.Unmarshal(raw, &req)
	}); err != nil {
		return nil, err
	}
	asked, err := readPublicKey(b.path("work", "request.pub"))
	if err != nil {
		return nil, fmt.Errorf("the request's working key: %w", err)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil {
		return nil, refuse("the file holds no certificate as ssh-keygen -s writes one: %v", err)
	}
	cert, err := protocol.CheckCertificate(b.id, key, b.now())
	switch {
	case errors.Is(err, protocol.ErrCertificateNotBegun):
		return nil, refuse("the certificate has not begun by this machine's clock: check this machine's clock against a time source you trust, or have its identity holder certify the request again")
	case errors.Is(err, protocol.ErrCertificateTime):
		return nil, refuse("the certificate has ended: its identity holder certifies the request again")
	case err != nil:
		return nil, refuse("the certificate breaks a rule of PROTOCOL.md §5.2 for this mailbox's identity: %v", err)
	}
	if !bytes.Equal(cert.WorkingKey().Marshal(), asked.Marshal()) {
		return nil, refuse("the certificate certifies another key than the working key of this mailbox's last request: dromas cert request prints that key, and a request made since replaces the one before")
	}
	// Longer than its server takes, its renewal would be refused, and a
	// certificate kept would be one no renewal can sign under.
	if cert.Lifetime() > life {
		return nil, refuse("the certificate runs %d seconds, longer than the %d its server takes: dromas cert request makes a new request, whose command its identity holder runs", int64(cert.Lifetime()/time.Second), int64(life/time.Second))
	}
	if cert.Serial() < req.Lowest {
		return nil, refuse("the certificate's serial is %d, below %d, the lowest serial this mailbox's request gave: its identity holder certifies the request again at that serial or above", cert.Serial(), req.Lowest)
	}
	own, err := b.revokedCerts(b.id.Address())
	if err != nil {
		return nil, err
	}
	// The request's lowest serial is above every revocation the mailbox kept
	// when it made it, so a revocation of this certificate is one kept since.
	if own != nil && own.Through > req.RevokedThrough {
		return nil, refuse("this mailbox's identity has revoked certificates above those the request knew of, so the request's working key may be stolen with the rest: dromas cert request makes a new request")
	}
	if from := cert.NotBefore().Add(protocol.MaxSkew); from.After(b.now()) {
		return nil, refuse("the certificate began less than an hour before this machine's clock, so this mailbox uses it only from %s (PROTOCOL.md §12.4): install it again then", from.UTC().Format(protocol.TimeLayout))
	}
	return cert, nil
}

// installedAt reports whether working key n is the key of the certificate
// request the mailbox holds: one InstallCertificate kept, its request not yet
// cleared.
func (b *Mailbox) installedAt(n uint64) bool {
	asked, err := os.ReadFile(b.path("work", "request.pub")) //nolint:gosec // a path inside the mailbox's own directory
	if err != nil {
		return false
	}
	at, err := os.ReadFile(b.workStem(n) + ".pub") //nolint:gosec // a path inside the mailbox's own directory
	return err == nil && bytes.Equal(bytes.TrimSpace(asked), bytes.TrimSpace(at))
}
