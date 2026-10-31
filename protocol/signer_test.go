package protocol

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/sshsig"
	"golang.org/x/crypto/ssh"
)

// rewrap is blob, an SSHSIG, with its signing key replaced by key: SSHSIG
// signs no signing-key field, so whoever holds a signature can write one.
func rewrap(t *testing.T, blob, key []byte) []byte {
	t.Helper()
	const head = len("SSHSIG") + 4 // then the signing key, as a string
	r := wireReader(blob[head:])
	if _, ok := r.string(); !ok {
		t.Fatal("no signing key in the blob")
	}
	out := appendWireString(append([]byte{}, blob[:head]...), key)
	return append(out, r...)
}

// VerifySigned takes a signature by either form a mailbox signs with, for an
// identity of either type: the identity's own key, byte for byte, which ranks
// 0, or a working key under a certificate the identity made, which ranks at
// its serial. Holding the signer to the address's mode is Admits'.
func TestVerifySignedTakesEitherForm(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for _, b := range []certMailbox{newCertMailbox(t), p256Mailbox(t)} {
		name := b.id.Type()
		msg, ns := []byte("a statement\n"), PrekeyNamespace(b.id.Address())
		own, err := Sign(b.identity, msg, ns)
		if err != nil {
			t.Fatal(err)
		}
		s, err := VerifySigned(b.id, msg, own, ns, now)
		if err != nil || !s.Identity() || s.Rank() != 0 || s.Certificate() != nil {
			t.Fatalf("%s: the identity's own signature: %+v, %v", name, s, err)
		}
		cert, err := NewCertificate(b.id, b.identity, b.work.PublicKey(), 1_800_000_000, now, testLifetime)
		if err != nil {
			t.Fatal(err)
		}
		certified, err := cert.Signer(b.work)
		if err != nil {
			t.Fatal(err)
		}
		under, err := Sign(certified, msg, ns)
		if err != nil {
			t.Fatal(err)
		}
		s, err = VerifySigned(b.id, msg, under, ns, now)
		if err != nil || s.Identity() || s.Rank() != 1_800_000_000 || s.Certificate().Serial() != cert.Serial() {
			t.Fatalf("%s: a working key's signature under its certificate: %v", name, err)
		}
		// The identity's form is its key, byte for byte: another mailbox's
		// identity is neither form for this one.
		other := newCertMailbox(t)
		stranger, err := Sign(other.identity, msg, ns)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := VerifySigned(b.id, msg, stranger, ns, now); !errors.Is(err, ErrCertificate) {
			t.Errorf("%s: another identity's own signature: %v", name, err)
		}
		// Either form holds for its message and namespace alone.
		for _, sig := range [][]byte{own, under} {
			if _, err := VerifySigned(b.id, []byte("another statement\n"), sig, ns, now); err == nil {
				t.Errorf("%s: verified over another message", name)
			}
			if _, err := VerifySigned(b.id, msg, sig, MessageNamespace(b.id.Address()), now); err == nil {
				t.Errorf("%s: verified in another namespace", name)
			}
		}
		// A certificate is checked at now; the identity's own signature has no dates.
		if _, err := VerifySigned(b.id, msg, under, ns, cert.NotAfter()); !errors.Is(err, ErrCertificateEnded) {
			t.Errorf("%s: under a certificate that has ended: %v", name, err)
		}
		if _, err := VerifySigned(b.id, msg, own, ns, LastDate); err != nil {
			t.Errorf("%s: the identity's own signature at the last date: %v", name, err)
		}
	}
}

// A verifier takes the identity's signature always, and a certificate above
// the revocation of certificates it holds for the address, every certificate
// where it holds none (PROTOCOL.md §5): a revocation of certificates never
// covers the identity, whatever it runs through.
func TestAdmitsTakesTheIdentityAndACertificateAboveTheRevocation(t *testing.T) {
	t.Parallel()
	b := newCertMailbox(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	under := func(serial uint64) *Signer {
		t.Helper()
		c, err := NewCertificate(b.id, b.identity, b.work.PublicKey(), serial, now, testLifetime)
		if err != nil {
			t.Fatal(err)
		}
		return &Signer{cert: c}
	}
	through := func(n uint64) *RevokedCertificates {
		t.Helper()
		r, err := NewRevokedCertificates(b.id.Address(), n)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, c := range []struct {
		name   string
		held   *RevokedCertificates
		signer *Signer
		want   error
	}{
		{"the identity, none held", nil, IdentitySigner(), nil},
		{"the identity, a revocation through 1 held", through(1), IdentitySigner(), nil},
		{"the identity, a revocation through the highest serial held", through(MaxSerial), IdentitySigner(), nil},
		{"a certificate, none held", nil, under(1), nil},
		{"the highest certificate, none held", nil, under(MaxSerial), nil},
		{"a certificate below N", through(5), under(4), ErrRevokedSigner},
		{"a certificate at N", through(5), under(5), ErrRevokedSigner},
		{"a certificate above N", through(5), under(6), nil},
		{"the highest certificate, through the highest serial", through(MaxSerial), under(MaxSerial), ErrRevokedSigner},
	} {
		if err := Admits(c.held, c.signer); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

// The re-wrap: SSHSIG signs no signing-key field, so the identity's own
// signature, rank 0, verifies under a certificate of the identity's own key at
// any serial: above a revocation of certificates, and above every statement
// the identity signed by (c, p), a rank it does not have. A certified key is
// never the identity's key (PROTOCOL.md §5), and that alone refuses it.
func TestTheIdentitysSignatureRewrappedInACertificateIsRefused(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	for _, b := range []certMailbox{newCertMailbox(t), p256Mailbox(t)} {
		name := b.id.Type()
		msg, ns := []byte("a statement\n"), PrekeyNamespace(b.id.Address())
		own, err := Sign(b.identity, msg, ns)
		if err != nil {
			t.Fatal(err)
		}
		held, err := NewRevokedCertificates(b.id.Address(), 5)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := VerifySigned(b.id, msg, own, ns, now)
		if err != nil || signer.Rank() != 0 || Admits(held, signer) != nil {
			t.Fatalf("%s: control: the identity's own signature, rank 0, with a revocation through 5 held: %v, %v", name, err, Admits(held, signer))
		}
		cert := &ssh.Certificate{
			Key:             b.identity.PublicKey(),
			Serial:          9,
			CertType:        ssh.UserCert,
			KeyId:           CertificateKeyID,
			ValidPrincipals: []string{b.id.Address().String()},
			ValidAfter:      uint64(now.Add(-MaxSkew).Unix()),
			ValidBefore:     uint64(now.Add(testLifetime).Unix()),
		}
		if err := cert.SignCert(rand.Reader, b.identity); err != nil {
			t.Fatal(err)
		}
		blob, err := Dearmor(own)
		if err != nil {
			t.Fatal(err)
		}
		wrapped := rewrap(t, blob, cert.Marshal())
		// Control: the signature verifies under the certificate it now names.
		if err := sshsig.Verify(cert, msg, wrapped, ns); err != nil {
			t.Fatalf("%s: control: the re-wrapped signature does not verify under its certificate: %v", name, err)
		}
		if s, err := VerifySigned(b.id, msg, Armor(wrapped), ns, now); !errors.Is(err, ErrCertificate) {
			t.Errorf("%s: the identity's signature re-wrapped at serial 9: %v, %v", name, s, err)
		}
		if _, err := CheckCertificate(b.id, cert, now); !errors.Is(err, ErrCertificate) {
			t.Errorf("%s: a certificate of the identity's own key: %v", name, err)
		}
	}
}

// A statement stands in its mailbox's order by (c, p): its signer's rank,
// then its own serial (PROTOCOL.md §5). PrekeyOrder reads a bare signing key
// as rank 0 and names the signer by the SHA-256 of its key's wire bytes,
// verifying nothing.
func TestPrekeyOrderIsTheSignersRankThenTheSerial(t *testing.T) {
	t.Parallel()
	b := newCertMailbox(t)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	st := func(serial uint64) []byte {
		return []byte(fmt.Sprintf("%s\n%s\n%d\nage1pq1x\n2026-12-01T00:00:00Z\n", PrekeyTag, b.id.Address(), serial))
	}
	cert, err := NewCertificate(b.id, b.identity, b.work.PublicKey(), 1_800_000_000, now, testLifetime)
	if err != nil {
		t.Fatal(err)
	}
	certified, err := cert.Signer(b.work)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		signer ssh.Signer
		key    ssh.PublicKey
		rank   uint64
	}{
		{"the identity", b.identity, b.identity.PublicKey(), 0},
		{"a certificate", certified, cert.PublicKey(), 1_800_000_000},
		{"a bare working key, which VerifyPrekey refuses", b.work, b.work.PublicKey(), 0},
	} {
		sig, err := Sign(c.signer, st(7), PrekeyNamespace(b.id.Address()))
		if err != nil {
			t.Fatal(err)
		}
		o, err := PrekeyOrder(st(7), sig)
		if err != nil || o != (Order{Rank: c.rank, Serial: 7, Signer: sha256.Sum256(c.key.Marshal())}) {
			t.Errorf("%s: %+v, %v", c.name, o, err)
		}
	}
	for _, c := range []struct {
		o, p Order
		less bool
	}{
		{Order{Rank: 0, Serial: MaxSerial}, Order{Rank: 1, Serial: 1}, true}, // any certificate passes the identity
		{Order{Rank: 3, Serial: 9}, Order{Rank: 4, Serial: 1}, true},         // a later certificate passes any serial
		{Order{Rank: 3, Serial: 2}, Order{Rank: 3, Serial: 3}, true},
		{Order{Rank: 0, Serial: 2}, Order{Rank: 0, Serial: 3}, true},
		{Order{Rank: 3, Serial: 3}, Order{Rank: 3, Serial: 3}, false},
		{Order{Rank: 4, Serial: 1}, Order{Rank: 3, Serial: 9}, false},
		{Order{Rank: 1, Serial: 1}, Order{Rank: 0, Serial: MaxSerial}, false},
	} {
		if got := c.o.Less(c.p); got != c.less {
			t.Errorf("(%d, %d) < (%d, %d): %v", c.o.Rank, c.o.Serial, c.p.Rank, c.p.Serial, got)
		}
	}
}

// A send names its statement by the SHA-256 of the statement's bytes, in
// lowercase hex, the form ParseSendQuery takes.
func TestStatementHashIsTheStatementsSHA256(t *testing.T) {
	t.Parallel()
	st := []byte(PrekeyTag + "\nx\n1\nage1pq1x\n2026-12-01T00:00:00Z\n")
	sum := sha256.Sum256(st)
	if got := StatementHash(st); got != fmt.Sprintf("%x", sum) || !statementGrammar.MatchString(got) {
		t.Errorf("StatementHash: %s", got)
	}
	if q, err := ParseSendQuery(SendQuery{Statement: StatementHash(st)}.Encode()); err != nil || q.Statement != StatementHash(st) {
		t.Errorf("a send's query does not carry the hash: %+v, %v", q, err)
	}
	if StatementHash(st[1:]) == StatementHash(st) {
		t.Error("two statements are named alike")
	}
}

// certificate_too_long is under its status, and carries the server's maximum.
func TestCertificateTooLongsCode(t *testing.T) {
	t.Parallel()
	if CodeCertificateTooLong.Status() != 400 {
		t.Errorf("certificate_too_long %d", CodeCertificateTooLong.Status())
	}
	var p Problem
	if err := Unmarshal([]byte(`{"type":"`+CodeCertificateTooLong.TypeURI()+`","code":"certificate_too_long","status":400,"max_certificate_lifetime":3891600}`), &p); err != nil || p.CodeAt(400) != CodeCertificateTooLong || p.MaxCertificateLifetime == nil || *p.MaxCertificateLifetime != 3891600 {
		t.Errorf("a certificate_too_long problem: %+v, %v", p, err)
	}
	if err := Unmarshal([]byte(`{"code":"certificate_too_long","status":400,"max_certificate_lifetime":-1}`), &p); err == nil {
		t.Error("a negative max_certificate_lifetime was taken")
	}
}
