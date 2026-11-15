package client_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/crypto/ssh"
)

// keptElsewhere is a mailbox under a certificate whose identity is kept
// elsewhere: its identity key file is moved to its holder, who signs with
// the key returned, and whose file holds raw.
func keptElsewhere(t *testing.T, e *env) (b *client.Mailbox, holder ssh.Signer, raw []byte) {
	t.Helper()
	b = e.mailbox()
	holder = identityKey(t, b)
	raw, err := os.ReadFile(b.Path("identity"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.Path("identity")); err != nil {
		t.Fatal(err)
	}
	if b, err = client.Open(b.Dir(), e.opts()); err != nil {
		t.Fatal(err)
	}
	return b, holder, raw
}

// certify is the holder's certificate for request r, at serial, made at now
// for the longest the request takes, as ssh-keygen -s writes one.
func certify(t *testing.T, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest, serial uint64, now time.Time) []byte {
	t.Helper()
	work, _, _, _, err := ssh.ParseAuthorizedKey([]byte(r.WorkingKey))
	if err != nil {
		t.Fatal(err)
	}
	c, err := protocol.NewCertificate(b.Identity(), holder, work, serial, now, time.Duration(r.Lifetime)*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return ssh.MarshalAuthorizedKey(c.PublicKey())
}

// signsUnder is the serial of the certificate a mailbox signs under, as
// its status says.
func signsUnder(t *testing.T, b *client.Mailbox) uint64 {
	t.Helper()
	st, err := b.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st.CertificateSerial
}

// A mailbox whose identity is kept elsewhere renews under the certificate it
// holds, asks its holder for the next one, and renews under the one the
// holder gives (PROTOCOL.md §12.7): its messages verify under it, the working
// keys below it go, and so does the request.
func TestAMailboxWhoseIdentityIsKeptElsewhereRenewsUnderEachCertificateItsHolderGives(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	e := newWorkEnv(t)
	a := e.mailbox()
	b, holder, _ := keptElsewhere(t, e)
	first := numberOf(t, b)
	e.clock.Add(8 * 24 * time.Hour)
	if res := receive(t, b); res.RenewErr != nil || signsUnder(t, b) != first || numberOf(t, b) == first {
		t.Fatalf("the renewal without the identity: %v; certificate %d, prekey %d", res.RenewErr, signsUnder(t, b), numberOf(t, b))
	}
	r, err := b.RequestCertificate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.Address != b.Address() || r.LowestSerial <= first || r.Lifetime <= 0 || !strings.HasPrefix(r.Command, "ssh-keygen -s identity ") || !strings.Contains(r.Command, " -n "+b.Address().String()+" ") {
		t.Fatalf("the request: %+v", r)
	}
	serial := protocol.SerialAt(e.clock.Now(), r.LowestSerial-1)
	p, err := b.InstallCertificate(ctx, certify(t, b, holder, r, serial, e.clock.Now()))
	if err != nil || p == nil || p.Rank != serial {
		t.Fatalf("install: %v; prekey %+v", err, p)
	}
	if got := signsUnder(t, b); got != serial {
		t.Fatalf("signs under certificate %d after the install, want %d", got, serial)
	}
	for _, name := range []string{"request", "request.pub", strconv.FormatUint(first, 10) + "-cert.pub"} {
		if _, err := os.Stat(b.Path("work", name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s is kept after the install: %v", name, err)
		}
	}
	receive(t, a)
	send(t, b, a, "under the holder's certificate")
	if res := receive(t, a); len(res.Delivered) != 1 || bodyOf(t, res.Delivered[0]) != "under the holder's certificate" {
		t.Fatalf("the reader: %+v", res)
	}
	e.clock.Add(8 * 24 * time.Hour)
	if res := receive(t, b); res.RenewErr != nil || signsUnder(t, b) != serial {
		t.Fatalf("the renewal after the install: %v; certificate %d", res.RenewErr, signsUnder(t, b))
	}
}

// The command a request prints is the ssh-keygen command that certifies it:
// run where the identity is, its certificate installs.
func TestTheRequestsCommandMakesTheCertificateTheMailboxInstalls(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen is not installed")
	}
	e := newWorkEnv(t)
	b, _, raw := keptElsewhere(t, e)
	r, err := b.RequestCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "request.pub"), []byte(r.WorkingKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(r.Command)
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // the command the request printed
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK=")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v: %s", r.Command, err, errb.String())
	}
	line, err := os.ReadFile(filepath.Join(dir, "request-cert.pub"))
	if err != nil {
		t.Fatal(err)
	}
	// ssh-keygen dates the certificate by the real clock: the mailbox's is
	// moved to it, past the hour the certificate begins early.
	e.clock.Add(time.Until(e.clock.Now())*-1 + time.Minute)
	if _, err := b.InstallCertificate(t.Context(), line); err != nil {
		t.Fatalf("install of what %s wrote: %v", r.Command, err)
	}
	if got := signsUnder(t, b); got < r.LowestSerial {
		t.Fatalf("signs under certificate %d, below the request's lowest serial %d", got, r.LowestSerial)
	}
}

// A certificate the holder gives is installed only where it meets the request
// it is for (PROTOCOL.md §12.7 step 3) and can be used now (§12.4); a refused
// one leaves the mailbox under the certificate it held, with nothing kept.
func TestACertificateFromItsHolderIsRefusedUnlessItMeetsTheRequest(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		make func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte
		want string
	}{
		{"for another request", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte {
			line := certify(t, b, holder, r, r.LowestSerial, e.clock.Now())
			if _, err := b.RequestCertificate(t.Context()); err != nil {
				t.Fatal(err)
			}
			return line
		}, "another key"},
		{"below the lowest serial", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte {
			return certify(t, b, holder, r, r.LowestSerial-1, e.clock.Now())
		}, "below"},
		{"begun less than an hour ago", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte {
			return certify(t, b, holder, r, r.LowestSerial, e.clock.Now().Add(protocol.MaxSkew))
		}, "less than an hour"},
		{"ended", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte {
			return certify(t, b, holder, r, r.LowestSerial, e.clock.Now().Add(-time.Duration(r.Lifetime)*time.Second))
		}, "ended"},
		{"by another identity", func(t *testing.T, e *env, b *client.Mailbox, _ ssh.Signer, r *client.CertificateRequest) []byte {
			other := e.mailbox()
			work, _, _, _, err := ssh.ParseAuthorizedKey([]byte(r.WorkingKey))
			if err != nil {
				t.Fatal(err)
			}
			c, err := protocol.NewCertificate(other.Identity(), identityKey(t, other), work, r.LowestSerial, e.clock.Now(), time.Duration(r.Lifetime)*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			return ssh.MarshalAuthorizedKey(c.PublicKey())
		}, "§5.2"},
		{"after a revocation made since the request", func(t *testing.T, e *env, b *client.Mailbox, holder ssh.Signer, r *client.CertificateRequest) []byte {
			rc, err := protocol.NewRevokedCertificates(b.Address(), r.LowestSerial)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := protocol.SignRevokedCertificates(holder, rc)
			if err != nil {
				t.Fatal(err)
			}
			rec := &protocol.Revocations{Identity: b.Identity().Document(), RevokedCertificates: rc.Statement(), RevokedCertificatesSignature: sig}
			if _, _, err := b.KeepRevocation(t.Context(), rec); err != nil {
				t.Fatal(err)
			}
			return certify(t, b, holder, r, r.LowestSerial+1, e.clock.Now())
		}, "after the request was made"},
		{"not a certificate", func(*testing.T, *env, *client.Mailbox, ssh.Signer, *client.CertificateRequest) []byte {
			return []byte("ssh-ed25519 AAAA\n")
		}, "no certificate"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newWorkEnv(t)
			b, holder, _ := keptElsewhere(t, e)
			before := signsUnder(t, b)
			r, err := b.RequestCertificate(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			line := c.make(t, e, b, holder, r)
			p, err := b.InstallCertificate(t.Context(), line)
			if p != nil || err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("install: %v; prekey %+v; want a refusal saying %q", err, p, c.want)
			}
			if got := signsUnder(t, b); got != before {
				t.Errorf("signs under certificate %d after a refused install, %d before", got, before)
			}
			kept, err := filepath.Glob(b.Path("work", "*-cert.pub"))
			if err != nil || len(kept) != 1 {
				t.Errorf("certificates kept after a refused install: %v %v", kept, err)
			}
		})
	}
}

// A mailbox whose identity signs asks for no certificate: it moves to
// certificates through dromas cert switch, with its identity.
func TestAMailboxWhoseIdentitySignsAsksForNoCertificate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	if r, err := b.RequestCertificate(t.Context()); r != nil || err == nil || !strings.Contains(err.Error(), "cert switch") {
		t.Fatalf("a request on a mailbox whose identity signs: %+v, %v", r, err)
	}
	if _, err := os.Stat(b.Path("work", "request")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused request made a working key: %v", err)
	}
}

// A mailbox whose identity is kept elsewhere signs no message once its
// certificate has protocol.TTL or less left, and renews nothing once it has
// ended, as identity_locked, each said by status with what to do; a
// certificate from its holder then brings it back.
func TestACertificateThatEndsWithItsIdentityElsewhereIsSaidAndMended(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	e := newWorkEnv(t)
	a := e.mailbox()
	b, holder, _ := keptElsewhere(t, e)
	st, err := b.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	until, err := protocol.ParseTime(st.CertificateSignsMessagesUntil)
	if err != nil {
		t.Fatalf("certificate_signs_messages_until %q: %v", st.CertificateSignsMessagesUntil, err)
	}
	end, err := protocol.ParseTime(st.CertificateNotAfter)
	if err != nil || !until.Equal(end.Add(-protocol.TTL)) {
		t.Fatalf("signs messages until %s, ends %s: %v", until, end, err)
	}
	for e.clock.Now().Add(8 * 24 * time.Hour).Before(until) {
		e.clock.Add(8 * 24 * time.Hour)
		if res := receive(t, b); res.RenewErr != nil {
			t.Fatalf("a renewal before the certificate's end: %v", res.RenewErr)
		}
	}
	e.clock.Add(until.Add(time.Hour).Sub(e.clock.Now()))
	if _, err := b.Send(ctx, a.Address(), "too late"); err == nil {
		t.Errorf("a send under a certificate with %s left was signed", end.Sub(e.clock.Now()))
	}
	saysRequest := func(when string) {
		t.Helper()
		st, err := b.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range st.Attention {
			if strings.Contains(s, "dromas cert request") {
				return
			}
		}
		t.Errorf("status %s: %q", when, st.Attention)
	}
	saysRequest("with too little left to sign messages")
	e.clock.Add(end.Add(time.Hour).Sub(e.clock.Now()))
	saysRequest("once the certificate has ended")
	_, err = b.Rekey(ctx)
	var locked *client.IdentityLockedError
	if !errors.As(err, &locked) || !strings.Contains(err.Error(), "dromas cert request") {
		t.Fatalf("a renewal once the certificate has ended: %v", err)
	}
	r, err := b.RequestCertificate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.InstallCertificate(ctx, certify(t, b, holder, r, protocol.SerialAt(e.clock.Now(), r.LowestSerial-1), e.clock.Now())); err != nil {
		t.Fatalf("install once the certificate has ended: %v", err)
	}
	receive(t, a)
	send(t, b, a, "mended")
	if res := receive(t, a); len(res.Delivered) != 1 || bodyOf(t, res.Delivered[0]) != "mended" {
		t.Fatalf("the reader after the install: %+v", res)
	}
}
