package protocol

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/dromas-dev/dromas-client/protocol/sktest"
	"github.com/dromas-dev/dromas-client/sshsig"
)

// The two mailboxes whose identities are security keys, and the name each
// type's vectors carry: D's is sk-ecdsa-sha2-nistp256@openssh.com, on RFC
// 9500's P-256 test key (input/d.p256), and E's sk-ssh-ed25519@openssh.com,
// on RFC 8032 §7.3's key (input/e.ed25519). Both certify the same working
// key, RFC 8032 §7.2's last (input/d-work.ed25519).
var skMailboxes = []struct{ mailbox, typ, name string }{
	{"d", IdentitySKP256, "sk-ecdsa"},
	{"e", IdentitySKEd25519, "sk-ed25519"},
}

// skVectorKey is a security-key mailbox's identity, as a security key in
// software made for SKApplication, from its published key.
func skVectorKey(t *testing.T, mailbox string) *sktest.Signer {
	t.Helper()
	var s *sktest.Signer
	var err error
	switch mailbox {
	case "d":
		sc := bufio.NewScanner(bytes.NewReader(read(t, "input/d.p256")))
		var lines [][]byte
		for sc.Scan() {
			b, err := hex.DecodeString(sc.Text())
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, b)
		}
		priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), lines[0])
		if err != nil {
			t.Fatal(err)
		}
		if point, _ := priv.PublicKey.Bytes(); !bytes.Equal(point, append([]byte{4}, lines[1]...)) {
			t.Fatal("d: the scalar does not give the public point")
		}
		s, err = sktest.New(priv, SKApplication)
		if err != nil {
			t.Fatal(err)
		}
	case "e":
		signer, _ := vectorKey(t, "e")
		priv := ed25519.NewKeyFromSeed(rawEd25519Seed(t, "e"))
		if !bytes.Equal(priv.Public().(ed25519.PublicKey), signer.PublicKey().(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey)) {
			t.Fatal("e: the seed does not give the public key")
		}
		s, err = sktest.New(priv, SKApplication)
	default:
		t.Fatalf("%s is no security-key mailbox", mailbox)
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// withFlags is a security-key mailbox's identity signing with the flags and
// the counter given.
func withFlags(t *testing.T, mailbox string, flags byte, counter uint32) *sktest.Signer {
	t.Helper()
	s := skVectorKey(t, mailbox)
	s.Flags, s.Counter = flags, counter
	return s
}

// rawEd25519Seed is the seed in input/<name>.ed25519.
func rawEd25519Seed(t *testing.T, name string) []byte {
	t.Helper()
	seed, err := hex.DecodeString(strings.Fields(string(read(t, "input/"+name+".ed25519")))[0])
	if err != nil {
		t.Fatal(err)
	}
	return seed
}

// skChanged signs as its security key does, and then changes the signature.
type skChanged struct {
	ssh.Signer
	change func(*ssh.Signature)
}

func (s skChanged) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	sig, err := s.Signer.Sign(rand, data)
	if err != nil {
		return nil, err
	}
	s.change(sig)
	return sig, nil
}

// u2fSigned is what a security key signs over data (PROTOCOL.u2f): the
// SHA-256 of the application, the flags, the counter, and the SHA-256 of the
// data, which webauthn replaces with the SHA-256 of its clientData.
func u2fSigned(flags byte, counter uint32, dataHash []byte) []byte {
	app := sha256.Sum256([]byte(SKApplication))
	out := binary.BigEndian.AppendUint32(append(app[:], flags), counter)
	return append(out, dataHash...)
}

// skNeutralR signs as E's security key does, with user presence, and with
// the neutral point as R (oddR): what RFC 8032's signer never makes, and the
// equation without the cofactor takes.
type skNeutralR struct {
	priv ed25519.PrivateKey
	pub  ssh.PublicKey
}

func (s skNeutralR) PublicKey() ssh.PublicKey { return s.pub }

func (s skNeutralR) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	odd, err := oddR{s.priv, false}.Sign(nil, u2fSigned(sktest.UserPresent, 0, sha256Of(data)))
	if err != nil {
		return nil, err
	}
	return &ssh.Signature{Format: ssh.KeyAlgoSKED25519, Blob: odd.Blob, Rest: []byte{sktest.UserPresent, 0, 0, 0, 0}}, nil
}

// plusL is an Ed25519 signature's 64 bytes with the group's order L added to
// its S, the last 32 bytes, little-endian: the other encoding of S, which
// RFC 8032 §5.1.7 refuses.
func plusL(sig []byte) []byte {
	l := []byte{0xed, 0xd3, 0xf5, 0x5c, 0x1a, 0x63, 0x12, 0x58, 0xd6, 0x9c, 0xf7, 0xa2, 0xde, 0xf9, 0xde, 0x14, 31: 0x10}
	out := bytes.Clone(sig)
	carry := 0
	for i := range 32 {
		v := int(out[32+i]) + int(l[i]) + carry
		out[32+i], carry = byte(v), v>>8
	}
	return out
}

// webauthnSigner signs as a WebAuthn authenticator does for D's key, in the
// form OpenSSH verifies for an sk-ecdsa key and calls
// webauthn-sk-ecdsa-sha2-nistp256@openssh.com (ssh-ecdsa-sk.c,
// webauthn_check_prepare_hash): with user presence, a clientData whose
// challenge is the data in base64url, and no extensions.
type webauthnSigner struct {
	priv *ecdsa.PrivateKey
	pub  ssh.PublicKey
}

const webauthnFormat = "webauthn-sk-ecdsa-sha2-nistp256@openssh.com"

func (w webauthnSigner) PublicKey() ssh.PublicKey { return w.pub }

func (w webauthnSigner) Sign(_ io.Reader, data []byte) (*ssh.Signature, error) {
	const origin = "ssh:" // short, so an SSHSIG of it is within the bound: a certificate's never is
	client := `{"type":"webauthn.get","challenge":"` + base64.RawURLEncoding.EncodeToString(data) + `","origin":"` + origin + `"}`
	ch := sha256.Sum256([]byte(client))
	h := sha256.Sum256(u2fSigned(sktest.UserPresent, 0, ch[:]))
	der, err := w.priv.Sign(nil, h[:], crypto.SHA256) // a nil rand: RFC 6979
	if err != nil {
		return nil, err
	}
	var rs struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &rs); err != nil {
		return nil, err
	}
	rest := ssh.Marshal(struct {
		Flags                     byte
		Counter                   uint32
		Origin, Client, Extension string
	}{sktest.UserPresent, 0, origin, client, ""})
	return &ssh.Signature{Format: webauthnFormat, Blob: ssh.Marshal(rs), Rest: rest}, nil
}

// skCert is the certificate of serial for the working key work, signed by
// the security key identity (or by sign, if not nil, over the certificate's
// bytes), as NewCertificate makes one at ./now for 45 days and the hour, with
// a nonce that is a hash of name: the same bytes each time.
func skCert(t *testing.T, id *Identity, identity ssh.Signer, work ssh.PublicKey, serial uint64, name string) *ssh.Certificate {
	t.Helper()
	after, _ := time.Parse(time.RFC3339, "2026-10-09T23:00:00Z") // as generate.sh's V_OK
	before, _ := time.Parse(time.RFC3339, "2026-11-24T00:00:00Z")
	nonce := sha512.Sum512([]byte("dromas vector: " + name))
	cert := &ssh.Certificate{
		Nonce:           nonce[:32],
		Key:             work,
		Serial:          serial,
		CertType:        ssh.UserCert,
		KeyId:           CertificateKeyID,
		ValidPrincipals: []string{id.Address().String()},
		ValidAfter:      uint64(after.Unix()),  //nolint:gosec // a time in 2026
		ValidBefore:     uint64(before.Unix()), //nolint:gosec // a time in 2026
		SignatureKey:    identity.PublicKey(),
	}
	sig, err := identity.Sign(nil, signedBytes(cert))
	if err != nil {
		t.Fatal(err)
	}
	cert.Signature = sig
	return cert
}

// skVectorFiles is every vector of the two security-key mailboxes, D and E,
// by its path under testdata/vectors: what stock tools cannot make, since no
// stock tool signs as a security key without one.
func skVectorFiles(t *testing.T) map[string][]byte {
	t.Helper()
	now := vectorNow(t)
	files := map[string][]byte{}
	b := vectorIdentity(t, "b")
	pq := strings.TrimSpace(string(read(t, "input/b-1.pq")))
	workKey, _ := vectorKey(t, "d-work")
	through, serial := vectorRevokedCerts(t).Through, vectorRank(t)
	sign := func(s ssh.Signer, msg []byte, ns string) []byte {
		t.Helper()
		sig, err := Sign(s, msg, ns)
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	under := func(cert *ssh.Certificate, work ssh.Signer) ssh.Signer {
		t.Helper()
		s, err := ssh.NewCertSigner(cert, work)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	statement := func(a Address, rank uint64) []byte {
		return (&Prekey{Address: a, Rank: rank, Serial: 1, Key: pq, NotAfter: now.Add(8 * 24 * time.Hour)}).Statement()
	}
	line := func(from *Identity) []byte {
		return fmt.Appendf(nil, `{"v":1,"from":"%s","to":"%s","sent_at":"%s","msg_id":"6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b","identity":"%s","body":"hello"}`+"\n",
			from.Address(), b.Address(), now.UTC().Format(TimeLayout), base64.StdEncoding.EncodeToString(from.Document()))
	}
	for _, v := range skMailboxes {
		m := v.mailbox
		identity := skVectorKey(t, m)
		id, err := IdentityOf(identity.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		a := id.Address()
		files[m+"/identity.doc"] = id.Document()
		files[m+"/address"] = []byte(a.String() + "\n")
		cert := skCert(t, id, identity, workKey.PublicKey(), serial, m+"/work-cert.pub")
		files[m+"/work-cert.pub"] = ssh.MarshalAuthorizedKey(cert)
		work := under(cert, workKey)

		// What the identity signs, and its statement and its message under
		// its certificate.
		files[m+"/prekey-1.txt"] = statement(a, 0)
		files[m+"/prekey-1.txt.sig"] = sign(identity, files[m+"/prekey-1.txt"], PrekeyNamespace(a))
		files["prekey/"+m+"-signed-under-its-certificate.txt"] = statement(a, serial)
		files["prekey/"+m+"-signed-under-its-certificate.txt.sig"] = sign(work, statement(a, serial), PrekeyNamespace(a))
		auth, notAfter, err := NewPass(identity, a, "dromas.example", now)
		if err != nil {
			t.Fatal(err)
		}
		pass, err := ParsePass(auth)
		if err != nil {
			t.Fatal(err)
		}
		files[m+"/pass.txt"] = PassStatement(a, "dromas.example", notAfter)
		files[m+"/pass.sig"] = pass.armored() // as the pass carries it, an ECDSA s written low
		files[m+"/pass.authorization"] = []byte(auth + "\n")
		files[m+"/revoke.txt"] = RevocationStatement(a)
		if files[m+"/revoke.sig"], err = SignRevocation(identity, a); err != nil {
			t.Fatal(err)
		}
		rc, err := NewRevokedCertificates(a, through)
		if err != nil {
			t.Fatal(err)
		}
		files[m+"/revoked-certs.txt"] = rc.Statement()
		if files[m+"/revoked-certs.sig"], err = SignRevokedCertificates(identity, rc); err != nil {
			t.Fatal(err)
		}
		files["plain/accept-"+v.name+"-sender.txt"] = append(line(id), sign(identity, line(id), MessageNamespace(b.Address()))...)
		files["plain/accept-"+v.name+"-sender-under-a-certificate.txt"] = append(line(id), sign(work, line(id), MessageNamespace(b.Address()))...)

		// The one form of its signature, and other forms, over its
		// statement in its namespace.
		st, ns := files[m+"/prekey-1.txt"], PrekeyNamespace(a)
		files["sig/"+m+"-ok-"+v.name+".sig"] = files[m+"/prekey-1.txt.sig"]
		noUP := skVectorKey(t, m)
		noUP.Flags = 0
		files["sig/"+m+"-bad-no-user-presence.sig"] = sign(noUP, st, ns)
		files["sig/"+m+"-bad-a-byte-after-the-counter.sig"] = sign(skChanged{identity, func(s *ssh.Signature) { s.Rest = append(s.Rest, 0) }}, st, ns)
		switch m {
		case "d":
			rs := func(s *ssh.Signature) (r, sv *big.Int) {
				var v struct{ R, S *big.Int }
				if err := ssh.Unmarshal(s.Blob, &v); err != nil {
					t.Fatal(err)
				}
				return v.R, v.S
			}
			files["sig/d-ok-the-other-s.sig"] = sign(skChanged{identity, func(s *ssh.Signature) {
				r, sv := rs(s)
				s.Blob = ssh.Marshal(struct{ R, S *big.Int }{r, new(big.Int).Sub(elliptic.P256().Params().N, sv)})
			}}, st, ns)
			files["sig/d-bad-r-padded.sig"] = sign(skChanged{identity, func(s *ssh.Signature) {
				var raw struct{ R, S []byte }
				if err := ssh.Unmarshal(s.Blob, &raw); err != nil {
					t.Fatal(err)
				}
				s.Blob = ssh.Marshal(struct{ R, S []byte }{append([]byte{0}, raw.R...), raw.S})
			}}, st, ns)
			priv, _ := ecdsa.ParseRawPrivateKey(elliptic.P256(), mustHex(t, strings.Fields(string(read(t, "input/d.p256")))[0]))
			wa := webauthnSigner{priv, identity.PublicKey()}
			files["sig/d-bad-webauthn.sig"] = sign(wa, st, ns)

			// A certificate of D's signed by a webauthn signature, good
			// under the certificate it carries.
			c := skCert(t, id, wa, workKey.PublicKey(), serial, "cert/d-bad-webauthn")
			files["cert/d-bad-webauthn.pub"] = ssh.MarshalAuthorizedKey(c)
			files["cert/d-bad-webauthn.txt"] = statement(a, serial)
			files["cert/d-bad-webauthn.sig"] = sign(under(c, workKey), statement(a, serial), ns)

			// Flags with user presence and other bits, as the Mac's Secure
			// Enclave writes them (0x21: UP and UV).
			files["sig/d-ok-flags-21.sig"] = sign(withFlags(t, m, 0x21, 0), st, ns)
			files["sig/d-bad-flags-fe.sig"] = sign(withFlags(t, m, 0xfe, 0), st, ns)
		case "e":
			// Flags with user presence and other bits set, among them ED,
			// which is never read; the highest counter; and every bit but
			// user presence.
			files["sig/e-ok-flags-05.sig"] = sign(withFlags(t, m, 0x05, 0), st, ns)
			files["sig/e-ok-flags-81.sig"] = sign(withFlags(t, m, 0x81, 0), st, ns)
			files["sig/e-ok-counter-ffffffff.sig"] = sign(withFlags(t, m, sktest.UserPresent, 0xffffffff), st, ns)
			files["sig/e-bad-flags-fe.sig"] = sign(withFlags(t, m, 0xfe, 0), st, ns)

			// A certificate of E's whose working key is a security key: the
			// working key's own Ed25519 key, as an sk-ssh-ed25519 key, which
			// signs under it.
			skWork, err := sktest.New(ed25519.NewKeyFromSeed(rawEd25519Seed(t, "d-work")), SKApplication)
			if err != nil {
				t.Fatal(err)
			}
			c := skCert(t, id, identity, skWork.PublicKey(), serial, "cert/e-bad-working-key-a-security-key")
			files["cert/e-bad-working-key-a-security-key.pub"] = ssh.MarshalAuthorizedKey(c)
			files["cert/e-bad-working-key-a-security-key.txt"] = statement(a, serial)
			files["cert/e-bad-working-key-a-security-key.sig"] = sign(under(c, skWork), statement(a, serial), ns)
			files["sig/e-bad-s-plus-l.sig"] = sign(skChanged{identity, func(s *ssh.Signature) { s.Blob = plusL(s.Blob) }}, st, ns)
			priv := ed25519.NewKeyFromSeed(rawEd25519Seed(t, "e"))
			files["sig/e-ok-r-small-order.sig"] = sign(skNeutralR{priv, identity.PublicKey()}, st, ns)
		}
		c := skCert(t, id, noUP, workKey.PublicKey(), serial, "cert/"+m+"-bad-no-user-presence")
		files["cert/"+m+"-bad-no-user-presence.pub"] = ssh.MarshalAuthorizedKey(c)
		files["cert/"+m+"-bad-no-user-presence.txt"] = statement(a, serial)
		files["cert/"+m+"-bad-no-user-presence.sig"] = sign(under(c, workKey), statement(a, serial), ns)

		// Documents of its type that break its rules: an application other
		// than ssh:, and the key checks of its plain type.
		key := skKeyFields(t, identity.PublicKey())
		doc := func(fields ...[]byte) []byte {
			var wire []byte
			for _, f := range fields {
				wire = appendWireString(wire, f)
			}
			return docOf(v.typ, wire)
		}
		withApp := func(app string) []byte {
			return doc(append(append([][]byte{[]byte(v.typ)}, key...), []byte(app))...)
		}
		for name, app := range map[string]string{"empty": "", "ssh-x": "ssh:x", "web": "web:", "ending-in-a-nul": "ssh:\x00"} {
			files["identity/"+v.name+"-application-"+name+".doc"] = withApp(app)
		}
		switch m {
		case "d":
			point := key[1]
			off := bytes.Clone(point)
			off[len(off)-1] ^= 1
			files["identity/sk-ecdsa-off-curve.doc"] = doc([]byte(v.typ), key[0], off, []byte(SKApplication))
			files["identity/sk-ecdsa-compressed.doc"] = doc([]byte(v.typ), key[0], append([]byte{2 + point[64]&1}, point[1:33]...), []byte(SKApplication))
			files["identity/sk-ecdsa-curve-name-mismatch.doc"] = doc([]byte(v.typ), []byte("nistp384"), point, []byte(SKApplication))
			wire := identity.PublicKey().Marshal()
			files["identity/sk-ecdsa-trailing-byte.doc"] = docOf(v.typ, append(bytes.Clone(wire), 0))
		case "e":
			files["identity/sk-ed25519-small-order.doc"] = doc([]byte(v.typ), mustHex(t, "0100000000000000000000000000000000000000000000000000000000000000"), []byte(SKApplication))
			files["identity/sk-ed25519-non-canonical.doc"] = doc([]byte(v.typ), mustHex(t, "f0ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f"), []byte(SKApplication)) // y = p + 3, as identity/non-canonical
		}
	}
	return files
}

// skKeyFields is a security key's wire fields between its type and its
// application: Ed25519's point, or P-256's curve name and point.
func skKeyFields(t *testing.T, k ssh.PublicKey) [][]byte {
	t.Helper()
	r := wireReader(k.Marshal())
	var f [][]byte
	for len(r) > 0 {
		s, ok := r.string()
		if !ok {
			t.Fatal("a key that is not SSH wire strings")
		}
		f = append(f, s)
	}
	return f[1 : len(f)-1]
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sha256Of(b []byte) []byte { h := sha256.Sum256(b); return h[:] }

// The vectors of D and E are what this test makes, the same bytes each time:
// every key is published, ECDSA signs deterministically and Ed25519 always
// does, and each nonce is a hash of the certificate's name.
// DROMAS_WRITE_VECTORS=1 writes them.
func TestVectorsSecurityKeysAreTheOnesMadeHere(t *testing.T) {
	files := skVectorFiles(t)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if os.Getenv("DROMAS_WRITE_VECTORS") != "" {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(vectors, name)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(vectors, name), files[name], 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := os.ReadFile(filepath.Join(vectors, name)); err != nil || !bytes.Equal(got, files[name]) {
			t.Errorf("%s is not what this test makes: %v", name, err)
		}
	}
}

// D's and E's identities are security keys, of one document length each,
// and everything an identity signs verifies under each, as under an Ed25519
// or a P-256 one: a statement, a pass, a revocation, a revocation of its
// certificates and a message, by the identity and under its certificate.
func TestVectorsSecurityKeyMailboxes(t *testing.T) {
	now := vectorNow(t)
	b := vectorIdentity(t, "b")
	for _, v := range skMailboxes {
		id := vectorIdentity(t, v.mailbox)
		if length := map[string]int{IdentitySKP256: 218, IdentitySKEd25519: 138}[v.typ]; id.Type() != v.typ || len(id.Document()) != length {
			t.Errorf("%s: a %s identity of %d bytes", v.mailbox, id.Type(), len(id.Document()))
		}
		if got, want := id.Address().String(), strings.TrimSpace(string(read(t, v.mailbox+"/address"))); got != want {
			t.Errorf("%s: address %s, want %s", v.mailbox, got, want)
		}
		m, a := v.mailbox, id.Address()
		if p, signer, err := VerifyPrekey(id, read(t, m+"/prekey-1.txt"), read(t, m+"/prekey-1.txt.sig"), now); err != nil || p.Rank != 0 || !signer.Identity() {
			t.Errorf("%s/prekey-1.txt: %v", m, err)
		}
		if p, signer, err := VerifyPrekey(id, read(t, "prekey/"+m+"-signed-under-its-certificate.txt"), read(t, "prekey/"+m+"-signed-under-its-certificate.txt.sig"), now); err != nil || p.Rank != vectorRank(t) || signer.Rank() != vectorRank(t) {
			t.Errorf("prekey/%s-signed-under-its-certificate: %v", m, err)
		}
		cert, err := CheckCertificate(id, vectorCertificate(t, m+"/work-cert.pub"), now)
		if err != nil || cert.Serial() != vectorRank(t) || cert.Lifetime() != testLifetime {
			t.Errorf("%s/work-cert.pub: %v", m, err)
		}
		pass, err := ParsePass(strings.TrimSuffix(string(read(t, m+"/pass.authorization")), "\n"))
		if err != nil {
			t.Fatal(err)
		}
		if signer, err := pass.Verify(id, "dromas.example", now); err != nil || !signer.Identity() || !bytes.Equal(pass.signature, read(t, m+"/pass.sig")) {
			t.Errorf("%s/pass.authorization: %v", m, err)
		}
		if got, err := VerifyRevocation(a, id.Document(), read(t, m+"/revoke.sig")); err != nil || got.Address() != a {
			t.Errorf("%s/revoke.sig: %v", m, err)
		}
		if r, err := VerifyRevokedCertificates(a, id.Document(), read(t, m+"/revoked-certs.txt"), read(t, m+"/revoked-certs.sig")); err != nil || r.Through >= cert.Serial() {
			t.Errorf("%s/revoked-certs: %v", m, err)
		}
		for _, name := range []string{"accept-" + v.name + "-sender", "accept-" + v.name + "-sender-under-a-certificate"} {
			if msg, code := verdictOf(t, read(t, "plain/"+name+".txt"), b, now); code != "" || msg.Sender.Address() != a {
				t.Errorf("plain/%s: %q", name, code)
			}
		}
	}
}

// Each other form of a security key's signature is refused: the form is what
// refuses the byte after the counter, the zero before r and the webauthn
// signature, which verifies under OpenSSH; flags without user presence are
// refused as a signature that does not verify, in the one form, which
// ssh-keygen accepts (TestVectorsByToolsNotOurs), and on a certificate's own
// signature by the certificate's check; so is a certificate whose signature
// is a webauthn one, which does not parse, and one of a working key that is a
// security key, under which its signature verifies.
func TestVectorsSecurityKeyRefusals(t *testing.T) {
	now := vectorNow(t)
	for _, v := range skMailboxes {
		m := v.mailbox
		id, st := vectorIdentity(t, m), read(t, m+"/prekey-1.txt")
		ns := PrekeyNamespace(id.Address())
		for name, form := range map[string]bool{"bad-no-user-presence": true, "bad-flags-fe": true, "bad-a-byte-after-the-counter": false, "bad-r-padded": false, "bad-webauthn": false, "bad-s-plus-l": true} {
			f := "sig/" + m + "-" + name + ".sig"
			if _, err := os.Stat(filepath.Join(vectors, f)); err != nil {
				continue // the vector is the other type's
			}
			if _, err := VerifySigned(id, st, read(t, f), ns, now); err == nil {
				t.Errorf("%s: taken", f)
			}
			if len(read(t, f)) > MaxSignature {
				t.Errorf("%s: longer than the bound, so its form is not what refuses it", f)
			}
			if _, err := Dearmor(read(t, f)); (err == nil) != form {
				t.Errorf("%s: in its one form: %v, want %v", f, err == nil, form)
			}
		}
		for _, name := range []string{"bad-no-user-presence", "bad-working-key-a-security-key", "bad-webauthn"} {
			stem := "cert/" + m + "-" + name
			if _, err := os.Stat(filepath.Join(vectors, stem+".pub")); err != nil {
				continue
			}
			sig := read(t, stem+".sig")
			if _, err := VerifySigned(id, read(t, stem+".txt"), sig, ns, now); err == nil {
				t.Errorf("%s: taken", stem)
			}
			if len(sig) > MaxSignature && name != "bad-webauthn" {
				t.Errorf("%s: longer than the bound, so the certificate is not what refuses it", stem)
			}
			key, _, _, _, err := ssh.ParseAuthorizedKey(read(t, stem+".pub"))
			if name == "bad-webauthn" {
				// A webauthn signature carries a hash of what it signs in
				// base64 in its clientData: on a certificate, it makes every
				// signature under it longer than the bound.
				if err == nil || len(sig) <= MaxSignature {
					t.Errorf("%s: a certificate whose signature is a webauthn one parses: %v, or its signature is within the bound", stem, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s: %v", stem, err)
			}
			if _, err := CheckCertificate(id, key, now); !errors.Is(err, ErrCertificate) {
				t.Errorf("%s: CheckCertificate: %v", stem, err)
			}
			// Control: the signature is good under the certificate it carries.
			if err := sshsig.Verify(key, read(t, stem+".txt"), armoredBlob(t, sig), ns); err != nil {
				t.Errorf("%s: control: the signature itself fails: %v", stem, err)
			}
		}
	}
}
