package client_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/crypto/ssh"
)

// The certificates a mailbox whose identity is kept elsewhere holds, and
// which of them signs (heldWorker, checkInstalled; the audit of #911, #931).

// A statement the mailbox did not make, under the certificate in use, shows
// that certificate's working key in other hands (PROTOCOL.md §12.7): the
// owner's renewal, with the identity elsewhere, signs nothing under it, at
// any prekey serial, and fails as identity_locked, for the holder's remedy
// (#928).
func TestAMailboxWhoseIdentityIsKeptElsewhereSignsNothingUnderACertificateAnotherHolds(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b, _, _ := keptElsewhere(t, e)
	n := numberOf(t, b)
	st, _ := e.srv.Prekey(b.Address())
	p, err := protocol.ParsePrekey(st)
	if err != nil {
		t.Fatal(err)
	}
	k, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p.Rank, p.Serial, p.Key = n, n+5, k.Recipient().String()
	sig, err := protocol.Sign(workerAt(t, b, n), p.Statement(), protocol.PrekeyNamespace(b.Address()))
	if err != nil {
		t.Fatal(err)
	}
	served := []byte(p.Statement())
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodGet && r.URL.Path == mailboxPath(b) {
			e.serve(w, r, next, func(m *protocol.MailboxResponse) { m.Prekey, m.PrekeySignature = served, sig })
			return
		}
		next.ServeHTTP(w, r)
	})
	e.clock.Add(25 * time.Hour)
	if res, _ := b.Receive(t.Context()); res == nil || res.NotMade == nil || !res.NotMade.OwnCertificate {
		t.Fatalf("recv shown a statement under its own certificate that it did not make: %+v", res)
	}
	q, err := b.Rekey(t.Context())
	var locked *client.IdentityLockedError
	if !errors.As(err, &locked) || q != nil {
		t.Fatalf("the owner's renewal under a certificate another holds: %+v, %v; want identity_locked", q, err)
	}
}

// Nor does a statement that waited under that certificate, signed before the
// served one: it is spent (signedPrekey), and the owner's renewal is under a
// new certificate where the identity is back, or fails as identity_locked
// where it is elsewhere (#928).
func TestAStatementWaitingUnderACertificateAnotherHoldsIsNotSent(t *testing.T) {
	t.Parallel()
	for _, back := range []bool{false, true} {
		t.Run(map[bool]string{false: "the identity elsewhere", true: "the identity back"}[back], func(t *testing.T) {
			t.Parallel()
			e := newWorkEnv(t)
			b, _, raw := keptElsewhere(t, e)
			n := numberOf(t, b)
			var refused atomic.Bool
			var thief atomic.Value
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if r.Method == http.MethodPut && r.URL.Path == mailboxPath(b)+"/prekey" && refused.CompareAndSwap(false, true) {
					http.Error(w, "", http.StatusServiceUnavailable)
					return
				}
				if v := thief.Load(); v != nil && r.Method == http.MethodGet && r.URL.Path == mailboxPath(b) {
					s := v.([2][]byte)
					e.serve(w, r, next, func(m *protocol.MailboxResponse) { m.Prekey, m.PrekeySignature = s[0], s[1] })
					return
				}
				next.ServeHTTP(w, r)
			})
			e.clock.Add(30 * time.Second)
			if _, err := b.Rekey(t.Context()); err == nil {
				t.Fatal("a renewal its server refused went through")
			}
			st, _ := e.srv.Prekey(b.Address())
			p, err := protocol.ParsePrekey(st)
			if err != nil {
				t.Fatal(err)
			}
			k, err := age.GenerateHybridIdentity()
			if err != nil {
				t.Fatal(err)
			}
			p.Rank, p.Serial, p.Key = n, n+1, k.Recipient().String()
			sig, err := protocol.Sign(workerAt(t, b, n), p.Statement(), protocol.PrekeyNamespace(b.Address()))
			if err != nil {
				t.Fatal(err)
			}
			thief.Store([2][]byte{[]byte(p.Statement()), sig})
			e.clock.Add(25 * time.Hour)
			if res, _ := b.Receive(t.Context()); res == nil || res.NotMade == nil || !res.NotMade.OwnCertificate {
				t.Fatalf("recv shown a statement under its own certificate that it did not make: %+v", res)
			}
			if back {
				if err := os.WriteFile(b.Path("identity"), raw, 0o600); err != nil {
					t.Fatal(err)
				}
				if b, err = client.Open(b.Dir(), e.opts()); err != nil {
					t.Fatal(err)
				}
			}
			q, err := b.Rekey(t.Context())
			var locked *client.IdentityLockedError
			switch {
			case back && (err != nil || q.Rank == n || q.Rank != q.Serial):
				t.Fatalf("the owner's renewal with the identity back: %+v, %v; want one under a new certificate", q, err)
			case !back && (!errors.As(err, &locked) || q != nil):
				t.Fatalf("the owner's renewal with the identity elsewhere: %+v, %v; want identity_locked", q, err)
			}
		})
	}
}

// A certificate for a request made before a revocation of the mailbox's
// certificates is refused, and nothing of it is kept, where the revocation
// was published from elsewhere and only the server holds it: the install
// reads the mailbox first, keeping the revocation (PROTOCOL.md §12.7, the
// remedy's step 2; #929). The brief's own case, a revocation the mailbox
// kept since the request, is TestACertificateFromItsHolderIsRefusedUnlessItMeetsTheRequest's.
func TestACertificateForARequestMadeBeforeARevocationOnlyItsServerHoldsIsRefused(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b, holder, _ := keptElsewhere(t, e)
	first := numberOf(t, b)
	r, err := b.RequestCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rc, err := protocol.NewRevokedCertificates(b.Address(), r.LowestSerial-1)
	if err != nil {
		t.Fatal(err)
	}
	rsig, err := protocol.SignRevokedCertificates(holder, rc)
	if err != nil {
		t.Fatal(err)
	}
	publish(t, e, &protocol.Revocations{Identity: b.Identity().Document(), RevokedCertificates: rc.Statement(), RevokedCertificatesSignature: rsig})
	serial := protocol.SerialAt(e.clock.Now(), r.LowestSerial-1)
	p, err := b.InstallCertificate(t.Context(), certify(t, b, holder, r, serial, e.clock.Now()))
	var refused *client.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "revoked certificates above those the request knew of") {
		t.Fatalf("an install under a request's working key made before a revocation: %+v, %v; want its refusal", p, err)
	}
	if exists(workFile(b, serial, "-cert.pub")) || signsUnder(t, b) != first {
		t.Errorf("the refused certificate %d is kept, or the mailbox signs under %d, not %d", serial, signsUnder(t, b), first)
	}
}

// A certificate that runs longer than its server takes, though no longer
// than the protocol's maximum, installs; the server refuses the renewal under
// it, and recv renews on under the certificate in use (PROTOCOL.md §12.7;
// #939 item 11).
func TestACertificateLongerThanItsServerTakesLeavesRecvRenewingUnderTheOneInUse(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	e.srv.SetPolicy(protocol.ServerDocument{MaxCertificateLifetime: uint64((30*24*time.Hour + time.Hour) / time.Second)})
	b, holder, _ := keptElsewhere(t, e)
	first := numberOf(t, b)
	r, err := b.RequestCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	work, _, _, _, err := ssh.ParseAuthorizedKey([]byte(r.WorkingKey))
	if err != nil {
		t.Fatal(err)
	}
	serial := protocol.SerialAt(e.clock.Now(), r.LowestSerial-1)
	c, err := protocol.NewCertificate(b.Identity(), holder, work, serial, e.clock.Now(), 40*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.InstallCertificate(t.Context(), ssh.MarshalAuthorizedKey(c.PublicKey()))
	if code := client.Code(err); code != protocol.CodeCertificateLifetimeTooLong {
		t.Fatalf("an install of a 40-day certificate at a 30-day server: %+v, %v; want its server's %s", p, err, protocol.CodeCertificateLifetimeTooLong)
	}
	e.clock.Add(25 * time.Hour)
	if res := receive(t, b); res.RenewErr != nil || res.Renewed == nil || res.Renewed.Rank != first {
		t.Fatalf("the next day's renewal: %+v, %v; want one under certificate %d", res.Renewed, res.RenewErr, first)
	}
}

// The owner's certificate lifetime is for the certificates the mailbox
// makes: a certificate its identity holder makes for longer, within what the
// server takes, installs (PROTOCOL.md §5.2).
func TestACertificateLongerThanTheOwnersLifetimeButNotTheServersInstalls(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	b, holder, _ := keptElsewhere(t, e)
	r, err := b.RequestCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opt := e.opts()
	opt.CertificateLifetime = 20 * 24 * time.Hour
	x, err := client.Open(b.Dir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	work, _, _, _, err := ssh.ParseAuthorizedKey([]byte(r.WorkingKey))
	if err != nil {
		t.Fatal(err)
	}
	serial := protocol.SerialAt(e.clock.Now(), r.LowestSerial-1)
	c, err := protocol.NewCertificate(b.Identity(), holder, work, serial, e.clock.Now(), 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := x.InstallCertificate(t.Context(), ssh.MarshalAuthorizedKey(c.PublicKey())); err != nil || p.Rank != serial {
		t.Fatalf("an install of a 30-day certificate where the owner's lifetime is 20 days: %+v, %v", p, err)
	}
}

// A renewal under a held certificate that the server took, whose answer the
// mailbox never had, is recorded at the mailbox's next read of itself, as one
// under the certificate made with it is (noteServed): the next renewal is
// taken, not refused as serial_too_low (#930).
func TestAHeldRenewalItsServerTookUnseenIsRecordedAtTheNextRead(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	e := newWorkEnv(t)
	b, _, _ := keptElsewhere(t, e)
	first := numberOf(t, b)
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "renewal taken", protocol.Address{})
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	took := servedSerial(t, e, x)
	if took == first || numberOf(t, x) != first {
		t.Fatalf("after the crash the server serves %d and the mailbox records %d: the test reaches no renewal taken unseen", took, numberOf(t, x))
	}
	e.clock.Add(2 * time.Second)
	if p, err := x.Rekey(t.Context()); err != nil || p.Serial <= took {
		t.Fatalf("the renewal after one taken unseen: %+v, %v", p, err)
	}
}

// A request's lowest serial is above every certificate serial the mailbox
// knows of for itself (PROTOCOL.md §5.2): a number a renewal took, a
// certificate it holds above the one in use, and one its server serves
// (highestCertificate; #931 item 7).
func TestARequestsLowestSerialIsAboveEveryCertificateSerialTheMailboxKnowsOf(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("long: -short skips it; scripts/check.sh full and CI run it")
	}
	for _, c := range []struct {
		name string
		// known leaves the mailbox knowing of a serial above the one in use,
		// and returns it.
		known func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer) uint64
	}{
		{"a renewal's number", func(t *testing.T, e *env, b *client.Mailbox, _ ssh.Signer) uint64 {
			e.clock.Add(time.Hour)
			crash(t, e, b, "rekey", "renewal begun", protocol.Address{})
			raw, ok := b.Record(t, "renewal")
			if !ok {
				t.Fatal("the crashed renewal recorded no number")
			}
			n, err := strconv.ParseUint(string(raw), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}},
		{"a certificate held", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer) uint64 {
			r, err := b.RequestCertificate(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			serial := protocol.SerialAt(e.clock.Now().Add(time.Hour), r.LowestSerial-1)
			file := filepath.Join(t.TempDir(), "request-cert.pub")
			if err := os.WriteFile(file, certify(t, b, holder, r, serial, e.clock.Now()), 0o600); err != nil {
				t.Fatal(err)
			}
			crashTo(t, e, b.Dir(), "cert install", "certificate installed", file)
			return serial
		}},
		{"a certificate its server serves", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer) uint64 {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			stranger, err := ssh.NewSignerFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			serial := numberOf(t, b) + 3600
			cert, err := protocol.NewCertificate(b.Identity(), holder, stranger.PublicKey(), serial, e.clock.Now(), testCertLife)
			if err != nil {
				t.Fatal(err)
			}
			signer, err := ssh.NewCertSigner(cert.PublicKey().(*ssh.Certificate), stranger)
			if err != nil {
				t.Fatal(err)
			}
			st, _ := e.srv.Prekey(b.Address())
			p, err := protocol.ParsePrekey(st)
			if err != nil {
				t.Fatal(err)
			}
			k, err := age.GenerateHybridIdentity()
			if err != nil {
				t.Fatal(err)
			}
			p.Rank, p.Serial, p.Key = serial, serial, k.Recipient().String()
			sig, err := protocol.Sign(signer, p.Statement(), protocol.PrekeyNamespace(b.Address()))
			if err != nil {
				t.Fatal(err)
			}
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if r.Method == http.MethodGet && r.URL.Path == mailboxPath(b) {
					fakeserver.WriteJSON(w, http.StatusOK, protocol.MailboxResponse{Identity: b.Identity().Document(), Prekey: p.Statement(), PrekeySignature: sig})
					return
				}
				next.ServeHTTP(w, r)
			})
			return serial
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newWorkEnv(t)
			b, holder, _ := keptElsewhere(t, e)
			known := c.known(t, e, b, holder)
			if known <= numberOf(t, b) {
				t.Fatalf("the mailbox knows of %d, not above the one in use, %d: the test shows nothing", known, numberOf(t, b))
			}
			x, err := client.Open(b.Dir(), e.opts())
			if err != nil {
				t.Fatal(err)
			}
			r, err := x.RequestCertificate(t.Context())
			if err != nil || r.LowestSerial <= known {
				t.Fatalf("a request once the mailbox knows of serial %d: %+v, %v; want its lowest serial above it", known, r, err)
			}
		})
	}
}
