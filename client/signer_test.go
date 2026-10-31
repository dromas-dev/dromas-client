package client_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// A mailbox's statements, passes and messages are signed by its identity or
// by a working key under a certificate it made (PROTOCOL.md §5): every
// verifier takes the identity's signature, and a certificate above the
// revocation of certificates it holds.

// certified reports whether mailbox b's status says it signs under a
// certificate.
func certified(t *testing.T, b *client.Mailbox) bool {
	t.Helper()
	st, err := b.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st.Certified
}

// A reader that has never met a sender takes its first message by either
// signer: the identity's, and a certificate's.
func TestAFirstContactReaderTakesEitherSigner(t *testing.T) {
	t.Parallel()
	for _, working := range []bool{false, true} {
		e := newEnv(t)
		e.working = working
		a := e.mailbox()
		e.working = false
		b := e.mailbox()
		if certified(t, a) != working {
			t.Fatalf("under a certificate %v: the sender's status says %v", working, certified(t, a))
		}
		send(t, a, b, "the first message")
		if res := receive(t, b); len(res.Delivered) != 1 || len(res.Refused) != 0 {
			t.Fatalf("under a certificate %v: the first message from a sender never met: %+v", working, res)
		}
	}
}

// certificateOf makes a certificate of a new working key, serial n, by the
// identity of mailbox b, and returns its signer: what whoever holds the
// identity can make.
func certificateOf(t *testing.T, e *env, b *client.Mailbox, n uint64) ssh.Signer {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	work, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := protocol.NewCertificate(b.Identity(), identityKey(t, b), work.PublicKey(), n, e.clock.Now(), testCertLife)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewCertSigner(cert.PublicKey().(*ssh.Certificate), work)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// upload sends a prekey statement at serial n signed by by to mailbox a's
// server, with a pass by pass, as anyone holding them can, and returns the
// code the server answers, or "" for none.
func upload(t *testing.T, e *env, a *client.Mailbox, by ssh.Signer, n uint64, pass ssh.Signer) protocol.Code {
	t.Helper()
	k, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p := &protocol.Prekey{Address: a.Address(), Serial: n, Key: k.Recipient().String(), NotAfter: e.clock.Now().Add(24 * time.Hour).Truncate(time.Second)}
	sig, err := protocol.Sign(by, p.Statement(), protocol.PrekeyNamespace(a.Address()))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(protocol.PrekeyRequest{Prekey: p.Statement(), PrekeySignature: sig})
	return put(t, e, a, "/prekey", body, pass)
}

// put PUTs body to mailbox a's path rest with a pass by pass, and returns
// the code the server answers, or "" for none.
func put(t *testing.T, e *env, a *client.Mailbox, rest string, body []byte, pass ssh.Signer) protocol.Code {
	t.Helper()
	h, _, err := protocol.NewPass(pass, a.Address(), strings.TrimPrefix(e.srv.URL, "http://"), e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPut, e.srv.URL+mailboxPath(a)+rest, bytes.NewReader(body))
	req.Header.Set("Authorization", h)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read only
	var p protocol.Problem
	if resp.StatusCode < 300 {
		return ""
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p.Code
}

// A server whose maximum leaves a certificate no time to sign messages, the
// TTL and the hour, refuses init --offline before anything is published
// (PROTOCOL.md §11.18); a mailbox whose identity signs goes on there.
func TestACertificateIsRefusedWhereTheServersMaximumLeavesNoRoom(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.srv.SetPolicy(protocol.ServerDocument{MaxCertificateLifetime: uint64((protocol.TTL + protocol.MaxSkew) / time.Second)})
	opt := e.opts()
	opt.WorkingKey = true
	from := len(e.srv.Requests())
	if _, err := client.Init(ctx, t.TempDir(), opt); err == nil || !strings.Contains(err.Error(), "no certificate is made here") {
		t.Fatalf("init --offline: %v", err)
	}
	for _, q := range e.srv.Requests()[from:] {
		if q.Method != http.MethodGet {
			t.Errorf("%s %s before the certificate was refused", q.Method, q.Path)
		}
	}
	// An hour more is room.
	e.srv.SetPolicy(protocol.ServerDocument{MaxCertificateLifetime: uint64((protocol.TTL+protocol.MaxSkew)/time.Second) + 3600})
	if b, err := client.Init(ctx, t.TempDir(), opt); err != nil || !certified(t, b) {
		t.Fatalf("init --offline with room: %v", err)
	}
	if _, err := client.Init(ctx, t.TempDir(), e.opts()); err != nil {
		t.Fatalf("init: %v", err)
	}
}

// A certificate runs for the longest its server's document names. A server
// whose maximum is shorter than that, its document read before it changed,
// answers certificate_too_long with its maximum, and the mailbox makes the
// statement again under a certificate of that one, once.
func TestACertificateTooLongIsMadeAgainAtTheServersMaximum(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	a := e.mailbox()
	e.clock.Add(2 * time.Second)
	short := fakeserver.MaxCertificateLifetime - 86400
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.URL.Path == protocol.DocumentPath {
			next.ServeHTTP(w, r) // the document names the longer one
			e.srv.SetPolicy(protocol.ServerDocument{MaxCertificateLifetime: short})
			return
		}
		next.ServeHTTP(w, r)
	})
	if _, err := a.Rekey(ctx); err != nil {
		t.Fatalf("a renewal answered certificate_too_long: %v", err)
	}
	st, sig := e.srv.Prekey(a.Address())
	_, by, err := protocol.VerifyPrekey(a.Identity(), st, sig, e.clock.Now())
	if err != nil || by.Certificate() == nil || by.Certificate().Lifetime() != time.Duration(short)*time.Second {
		t.Fatalf("the statement taken: %v, %+v", err, by)
	}
	words := (&client.APIError{Status: 400, Problem: protocol.Problem{Code: protocol.CodeCertificateTooLong, MaxCertificateLifetime: &short}}).Error()
	if !strings.Contains(words, strconv.FormatUint(short, 10)+" seconds") {
		t.Errorf("certificate_too_long says %q", words)
	}
}

// The server keeps a mailbox until the later of its prekey's not_after plus
// the TTL and the end of the latest certificate it took, and never lowers it.
func TestTheServerKeepsAMailboxByItsLatestCertificate(t *testing.T) {
	t.Parallel()
	e := newWorkEnv(t)
	// A certificate longer than a prekey and the TTL: its end is the later.
	long := 200 * 24 * time.Hour
	e.srv.SetPolicy(protocol.ServerDocument{MaxCertificateLifetime: uint64(long / time.Second)})
	a := e.mailbox()
	kept := e.srv.KeptUntil(a.Address())
	if want := e.clock.Now().Add(-protocol.MaxSkew).Add(long); kept.Before(want) {
		t.Errorf("kept until %s, before its certificate ends, %s", kept, want)
	}
	e.clock.Add(time.Hour)
	if err := a.SetPrekeyLifetime(t.Context(), client.MinPrekeyLifetime); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Rekey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again := e.srv.KeptUntil(a.Address()); again.Before(kept) {
		t.Errorf("kept until %s, lowered from %s", again, kept)
	}
}

// A mailbox whose identity signs, served a statement its identity signed that
// it did not make, says so and recommends revoking the address; recv renews
// nothing above it, its owner's renewal does, and its outbox does not wait:
// no certificate of its own is in doubt (PROTOCOL.md §12.4).
func TestAnIdentityStatementItDidNotMake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	st, _ := e.srv.Prekey(a.Address())
	p, err := protocol.ParsePrekey(st)
	if err != nil {
		t.Fatal(err)
	}
	k, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	p.Serial, p.Key = p.Serial+5, k.Recipient().String()
	sig, err := protocol.Sign(identityKey(t, a), p.Statement(), protocol.PrekeyNamespace(a.Address()))
	if err != nil {
		t.Fatal(err)
	}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if x, ok := forge.ReadOf(r); ok && x == a.Address() {
			e.serve(w, r, next, func(br *protocol.MailboxResponse) { br.Prekey, br.PrekeySignature = p.Statement(), sig })
			return
		}
		next.ServeHTTP(w, r)
	})
	e.clock.Add(25 * time.Hour)
	res, err := a.Receive(ctx)
	if err != nil || res.NotMade == nil || res.Renewed != nil || res.NotMade.CertificateSerial != 0 {
		t.Fatalf("recv: %+v, %v", res, err)
	}
	if words := res.NotMade.Words(); !strings.Contains(words, "identity is in other hands") || strings.Contains(words, "certificate 0") {
		t.Errorf("its words: %s", words)
	}
	e.srv.SetMiddle(nil)
	if _, err := a.Send(ctx, b.Address(), "the outbox does not wait"); err != nil {
		t.Errorf("a send: %v", err)
	}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if x, ok := forge.ReadOf(r); ok && x == a.Address() {
			e.serve(w, r, next, func(br *protocol.MailboxResponse) { br.Prekey, br.PrekeySignature = p.Statement(), sig })
			return
		}
		next.ServeHTTP(w, r)
	})
	if q, err := a.Rekey(ctx); err != nil || q.Serial <= p.Serial {
		t.Errorf("the owner's renewal: %+v, %v", q, err)
	}
}

// After a revocation of certificates both kinds of revocation still verify
// and are kept: they are the identity's, never ranked (PROTOCOL.md §5).
func TestRevocationsVerifyAfterARevocationOfCertificates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	a, b := e.mailbox(), e.mailbox()
	e.clock.Add(2 * time.Second)
	if _, err := a.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	certs, err := a.RevokeEarlier(ctx)
	if err != nil || certs == nil {
		t.Fatalf("a revocation of certificates: %+v, %v", certs, err)
	}
	if held, err := b.Ask(ctx, a.Address()); err != nil || held.Certificates == nil || held.Certificates.Through != certs.Through {
		t.Fatalf("its peer asks: %+v, %v", held, err)
	}
	rec, err := a.Revocation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Revoke(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if held, err := b.Ask(ctx, a.Address()); err != nil || !held.Revoked {
		t.Fatalf("its peer asks after the address's revocation: %+v, %v", held, err)
	}
}

// A reader takes the identity's signature from a sender that also has
// certificates, before and after it holds the sender's revocation of
// certificates; a certificate at or below that revocation is
// certificate_revoked, and kept as the identity it claims alone (PROTOCOL.md
// §12.3 check 7).
func TestAReaderTakesTheIdentityAndACertificateAboveTheRevocation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	a := e.mailbox()
	e.working = false
	b := e.mailbox()
	old := worker(t, a)
	messageAs(t, e, a, b, identityKey(t, a), "by the identity of a sender under a certificate")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("the identity's message from a sender under a certificate: %+v", res)
	}
	e.clock.Add(2 * time.Second)
	if _, err := a.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if certs, err := a.RevokeEarlier(ctx); err != nil || certs == nil || !certs.Revokes(old.PublicKey().(*ssh.Certificate).Serial) {
		t.Fatalf("revoke earlier: %+v, %v", certs, err)
	}
	messageAs(t, e, a, b, identityKey(t, a), "by the identity, after a revocation of certificates")
	messageAs(t, e, a, b, old, "under a revoked certificate")
	send(t, a, b, "under the current certificate")
	res := receive(t, b)
	if len(res.Delivered) != 2 || len(res.Refused) != 1 || res.Refused[0].Code != client.RefusedCertificate {
		t.Fatalf("after a revocation of certificates: %+v", res)
	}
	for _, d := range res.Delivered {
		if bodyOf(t, d) == "under a revoked certificate" {
			t.Errorf("a message under a revoked certificate was delivered")
		}
	}
	if !keptList(b, a.Address()) {
		t.Error("the reader keeps no revocation of the sender's certificates")
	}
	if q := refusedAs(t, b, res.Refused[0].ID); q.SignedBy != "" || q.ClaimsIdentity != a.Address().String() || q.CertificateSerial != 0 {
		t.Errorf("kept as %+v: the identity it claims alone, since the reader holds a revocation that covers its certificate", q)
	}
}

// Statements are ordered by (c, p), the identity at 0, so once a statement
// under a certificate is taken an identity statement stands below it: the
// server refuses one as certificate_not_current (PROTOCOL.md §11.9), and a
// sender that saw the certificate's refuses one served as a rollback, posting
// nothing (PROTOCOL.md §12.2).
func TestAnIdentityStatementAfterACertificatesIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	a := e.mailbox()
	e.working = false
	b := e.mailbox()
	send(t, b, a, "to a mailbox under a certificate")
	if code := upload(t, e, a, identityKey(t, a), numberOf(t, a)+10, identityKey(t, a)); code != protocol.CodeCertificateNotCurrent {
		t.Errorf("an identity statement at a mailbox under a certificate: %q, want certificate_not_current", code)
	}
	if servedCertificate(t, e, a) != numberOf(t, a) {
		t.Fatalf("the server serves %d, the mailbox published %d", servedCertificate(t, e, a), numberOf(t, a))
	}
	st, _ := e.srv.Prekey(a.Address())
	p, err := protocol.ParsePrekey(st)
	if err != nil {
		t.Fatal(err)
	}
	p.Serial += 10
	sig, err := protocol.Sign(identityKey(t, a), p.Statement(), protocol.PrekeyNamespace(a.Address()))
	if err != nil {
		t.Fatal(err)
	}
	op := watchWith(e, func(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
		if x, ok := forge.ReadOf(r); ok && x == a.Address() {
			e.serve(w, r, next, func(br *protocol.MailboxResponse) { br.Prekey, br.PrekeySignature = p.Statement(), sig })
			return true
		}
		return false
	})
	if _, err := b.Send(ctx, a.Address(), "never posted"); err == nil || !strings.Contains(err.Error(), "rollback") || !strings.Contains(err.Error(), "signed by its identity") {
		t.Fatalf("a send to an identity statement below a certificate already seen: %v", err)
	}
	if op.count(http.MethodPost, postTo(a)) != 0 {
		t.Fatal("it was posted")
	}
}

// init --offline registers the mailbox under a certificate at once: its
// first statement is under one, and nothing revokes certificates, at the
// mailbox or at its server.
func TestInitOfflineRegistersUnderACertificate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newWorkEnv(t)
	from := len(e.srv.Requests())
	a := e.mailbox()
	for _, q := range e.srv.Requests()[from:] {
		if strings.HasSuffix(q.Path, "/revocations") {
			t.Errorf("init --offline: %s %s", q.Method, q.Path)
		}
	}
	if n := numberOf(t, a); n == 0 || servedCertificate(t, e, a) != n || !certified(t, a) {
		t.Fatalf("init --offline: the server serves certificate %d, the mailbox published %d, certified %v", servedCertificate(t, e, a), n, certified(t, a))
	}
	if keptThrough(t, a) != 0 {
		t.Errorf("the mailbox keeps a revocation of its certificates through %d", keptThrough(t, a))
	}
	e.working = false
	b := e.mailbox()
	if held, err := b.Ask(ctx, a.Address()); err != nil || held.Certificates != nil || held.Revoked {
		t.Errorf("its server holds: %+v, %v", held, err)
	}
}

// A server takes the identity's pass always, and a certificate's only under
// exactly the current statement's certificate (PROTOCOL.md §8): another is
// certificate_not_current, naming the current one where it is a certificate
// and none where it is the identity, and one at or below the revocation of
// certificates is certificate_revoked, as is a statement under one.
func TestThePassIsTheIdentitysOrTheCurrentCertificates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	acl := []byte(`{"allow":["*"],"deny":[]}`)
	e := newWorkEnv(t)
	a := e.mailbox()
	if code := put(t, e, a, "/acl", acl, identityKey(t, a)); code != "" {
		t.Errorf("the identity's pass at a mailbox under a certificate: %q", code)
	}
	if code := put(t, e, a, "/acl", acl, worker(t, a)); code != "" {
		t.Errorf("the current certificate's pass: %q", code)
	}
	other := certificateOf(t, e, a, numberOf(t, a)+5)
	if code, p := passAt(t, e, a, other); code != protocol.CodeCertificateNotCurrent || p.CertificateSerial == nil || *p.CertificateSerial != numberOf(t, a) {
		t.Errorf("another certificate's pass: %q, %+v", code, p)
	}
	old := worker(t, a)
	e.clock.Add(2 * time.Second)
	if _, err := a.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RevokeEarlier(ctx); err != nil {
		t.Fatal(err)
	}
	if code := put(t, e, a, "/acl", acl, old); code != protocol.CodeCertificateRevoked {
		t.Errorf("a revoked certificate's pass: %q", code)
	}
	if code := upload(t, e, a, old, numberOf(t, a)+10, old); code != protocol.CodeCertificateRevoked {
		t.Errorf("a statement under a revoked certificate: %q", code)
	}
	if code := put(t, e, a, "/acl", acl, identityKey(t, a)); code != "" {
		t.Errorf("the identity's pass after a revocation of certificates: %q", code)
	}

	e.working = false
	b := e.mailbox()
	code, p := passAt(t, e, b, certificateOf(t, e, b, protocol.SerialAt(e.clock.Now(), 0)+5))
	if code != protocol.CodeCertificateNotCurrent || p.CertificateSerial != nil {
		t.Errorf("a certificate's pass at a mailbox whose identity signs: %q, %+v", code, p)
	}
	if words := (&client.APIError{Status: 401, Problem: p}).Error(); strings.Contains(words, "certificate 0") || strings.Contains(words, ", certificate ") {
		t.Errorf("certificate_not_current with no certificate says %q", words)
	}
}

// passAt reads mailbox a's ACL with a pass by pass, and returns the problem
// the server answers, if any.
func passAt(t *testing.T, e *env, a *client.Mailbox, pass ssh.Signer) (protocol.Code, protocol.Problem) {
	t.Helper()
	h, _, err := protocol.NewPass(pass, a.Address(), strings.TrimPrefix(e.srv.URL, "http://"), e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+mailboxPath(a)+"/acl", nil)
	req.Header.Set("Authorization", h)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // read only
	var p protocol.Problem
	if resp.StatusCode < 300 {
		return "", p
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p.Code, p
}

// dromas cert switch (Mailbox.RenewUnderCertificate) renews a mailbox whose
// identity signs under a certificate, taken at its server above the
// identity's statement with nothing revoked: what its identity signed before
// stays good, so a message unread at the switch is delivered, and the
// mailbox signs under certificates from then on.
func TestASwitchRenewsUnderACertificate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "signed by the identity, unread at the switch")
	e.clock.Add(2 * time.Second)
	p, err := a.RenewUnderCertificate(ctx)
	if err != nil || p == nil || servedCertificate(t, e, a) != numberOf(t, a) || numberOf(t, a) == 0 || !certified(t, a) || keptThrough(t, a) != 0 {
		t.Fatalf("the switch: %+v, %v; the server serves %d, the mailbox published %d, kept through %d", p, err, servedCertificate(t, e, a), numberOf(t, a), keptThrough(t, a))
	}
	send(t, a, b, "under the certificate")
	if res := receive(t, b); len(res.Delivered) != 2 || len(res.Refused) != 0 {
		t.Errorf("the reader: %+v", res)
	}
	if code := upload(t, e, a, identityKey(t, a), numberOf(t, a)+10, identityKey(t, a)); code != protocol.CodeCertificateNotCurrent {
		t.Errorf("an identity statement after the switch: %q", code)
	}
	first := numberOf(t, a)
	e.clock.Add(2 * time.Second)
	if _, err := a.Rekey(ctx); err != nil || servedCertificate(t, e, a) != numberOf(t, a) || numberOf(t, a) <= first {
		t.Errorf("a renewal after the switch: %v; the server serves %d", err, servedCertificate(t, e, a))
	}
}

// A restore reads its mailbox at its server and renews under a new
// certificate where the server serves a statement under one, or where it is
// told to (init --offline); otherwise its identity signs.
func TestARestoreRenewsUnderACertificateWhereItsServerServesOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, working := range []bool{false, true} {
		for _, told := range []bool{false, true} {
			e := newEnv(t)
			e.working = working
			a := e.mailbox()
			e.clock.Add(time.Minute)
			r := restore(t, e, a)
			opt := e.opts()
			opt.WorkingKey = told
			if _, err := client.Init(ctx, r.Dir(), opt); err != nil {
				t.Fatalf("under a certificate %v, told %v: %v", working, told, err)
			}
			want := working || told
			if n := numberOf(t, r); servedSerial(t, e, a) != n || (servedCertificate(t, e, a) == n) != want || certified(t, r) != want {
				t.Errorf("under a certificate %v, told %v: the server serves serial %d by rank %d, the restore published %d", working, told, servedSerial(t, e, a), servedCertificate(t, e, a), n)
			}
		}
	}
}

// cert revoke-earlier, and its second half alone, are refused for a mailbox
// that has no certificate, before anything is signed or published.
func TestRevokeEarlierIsRefusedWithNoCertificate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a := e.mailbox()
	from := len(e.srv.Requests())
	if _, err := a.RenewAndRevoke(ctx); err == nil || !strings.Contains(err.Error(), "no certificate to revoke") {
		t.Errorf("the remedy: %v", err)
	}
	if _, err := a.RevokeEarlier(ctx); err == nil || !strings.Contains(err.Error(), "no certificate to revoke") {
		t.Errorf("revoke earlier: %v", err)
	}
	for _, q := range e.srv.Requests()[from:] {
		if q.Method != http.MethodGet {
			t.Errorf("%s %s", q.Method, q.Path)
		}
	}
}

// A renewal its identity signed and its server never took is spent when the
// next renewal is under a certificate (cert switch): the switch publishes a
// new statement under one, never the identity's waiting statement.
func TestASwitchSpendsAWaitingIdentityStatement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a := e.mailbox()
	e.clock.Add(2 * time.Second)
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && r.URL.Path == mailboxPath(a)+"/prekey" {
			forge.Refuse(w, protocol.CodeUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
	if _, err := a.Rekey(ctx); err == nil {
		t.Fatal("the renewal was taken")
	}
	e.srv.SetMiddle(nil)
	waiting := begunAt(t, a)
	if waiting <= numberOf(t, a) {
		t.Fatalf("no renewal waits: %d, published %d", waiting, numberOf(t, a))
	}
	e.clock.Add(2 * time.Second)
	p, err := a.RenewUnderCertificate(ctx)
	if err != nil || p == nil || p.Serial == waiting || servedCertificate(t, e, a) != p.Serial {
		t.Fatalf("the switch: %+v, %v; waiting %d, the server serves rank %d", p, err, waiting, servedCertificate(t, e, a))
	}
}

// A revocation of certificates covers no identity (PROTOCOL.md §10.2): a
// mailbox whose identity signs, handed one of its own, renews nothing for it
// and goes on sending, and its reader takes what it sends.
func TestARevocationOfCertificatesLeavesTheIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	rec := certsRecord(t, a.Dir(), protocol.SerialAt(e.clock.Now(), 0)+86400)
	publish(t, e, rec)
	if _, _, err := a.KeepRevocation(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if keptThrough(t, a) == 0 {
		t.Fatal("the revocation was not kept")
	}
	if res, err := a.Receive(ctx); err != nil || res.Renewed != nil || res.RenewErr != nil {
		t.Fatalf("recv: %+v, %v", res, err)
	}
	send(t, a, b, "by the identity, its certificates revoked")
	if res := receive(t, b); len(res.Delivered) != 1 || !keptList(b, a.Address()) {
		t.Fatalf("the reader: %+v", res)
	}
}
