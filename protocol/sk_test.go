package protocol

import (
	"crypto/elliptic"
	"crypto/rand"
	"math/big"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// A security key's signature in its one form ends with exactly five bytes,
// its flags and its counter (PROTOCOL.md §3.2): with fewer or more after its
// bytes, it is no signature in its one form, by the identity or on a
// certificate it signed.
func TestASecurityKeysSignatureEndsWithItsFlagsAndCounter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	for _, ty := range []string{IdentitySKEd25519, IdentitySKP256} {
		b := skMailbox(t, ty)
		ns := PrekeyNamespace(b.id.Address())
		for n := range 8 {
			signer := skChanged{b.identity, func(s *ssh.Signature) {
				rest := make([]byte, n) // the flags and counter cut short, or zeros after them
				copy(rest, s.Rest)
				s.Rest = rest
			}}
			sig, err := Sign(signer, []byte("a statement"), ns)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifySigned(b.id, []byte("a statement"), sig, ns, now); (err == nil) != (n == 5) {
				t.Errorf("%s with %d bytes after its own: %v", ty, n, err)
			}
			cert := b.rawCert(now)
			if err := cert.SignCert(rand.Reader, signer); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckCertificate(b.id, cert, now); (err == nil) != (n == 5) {
				t.Errorf("%s's certificate with %d bytes after its signature's own: %v", ty, n, err)
			}
		}
	}
}

// A pass is written with an ECDSA s low, a security key's too, and with the
// key's flags and counter as they were: the pass written from either s is
// one pass, and it verifies.
func TestAnSKECDSAPassIsWrittenWithItsSLow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	b := skMailbox(t, IdentitySKP256)
	auth, _, err := NewPass(b.identity, b.id.Address(), "dromas.example", now)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParsePass(auth)
	if err != nil {
		t.Fatal(err)
	}
	high := skChanged{b.identity, func(s *ssh.Signature) {
		var rs struct{ R, S *big.Int }
		if err := ssh.Unmarshal(s.Blob, &rs); err != nil {
			t.Fatal(err)
		}
		if n := elliptic.P256().Params().N; rs.S.Cmp(new(big.Int).Rsh(n, 1)) <= 0 {
			rs.S.Sub(n, rs.S)
		}
		s.Blob = ssh.Marshal(rs)
	}}
	sig, err := Sign(high, PassStatement(b.id.Address(), "dromas.example", p.NotAfter), PassNamespace(b.id.Address()))
	if err != nil {
		t.Fatal(err)
	}
	q := &Pass{Address: p.Address, NotAfter: p.NotAfter, signature: sig}
	if q.Authorization() != auth {
		t.Errorf("the pass with s high is written %q, and with s low %q", q.Authorization(), auth)
	}
	if signer, err := p.Verify(b.id, "dromas.example", now); err != nil || !signer.Identity() {
		t.Errorf("the pass does not verify: %v", err)
	}
	if _, err := q.Verify(b.id, "dromas.example", now); err != nil {
		t.Errorf("the pass with s high does not verify: %v", err)
	}
}
