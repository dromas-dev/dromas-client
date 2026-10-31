package protocol

import (
	"bytes"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math/big"
	"strings"

	"github.com/dromas-dev/dromas-client/sshsig"
	"golang.org/x/crypto/ssh"
)

// Signatures travel exactly as "ssh-keygen -Y sign" writes them: an armored
// SSHSIG blob. These are the armor's lines.
const (
	armorBegin = "-----BEGIN SSH SIGNATURE-----\n"
	armorEnd   = "-----END SSH SIGNATURE-----"
	armorWidth = 70 // ssh-keygen's line length
)

// MaxSignature is the most bytes an armored signature may be for a mailbox of any
// identity key type defined here: the largest of their bounds
// (SignatureBound). A field that carries one counts every byte it holds; in a
// message it is the block from the BEGIN line through the END line and its
// line feed. Dearmor refuses a longer one before reading it, so every
// verifier here does, before it knows whose signature it is.
const MaxSignature = 975

// SignatureBound returns the most bytes an armored signature may be for a mailbox
// whose identity is of key type t (PROTOCOL.md §3.2): what the mailbox signs, by its
// identity or by a working key under a certificate its identity signed. It is
// the largest signature a mailbox of that type makes in the one form, in any
// namespace, under a certificate as NewCertificate makes one: the one form
// fixes every byte but the namespace's and, for an ECDSA key, how many bytes
// r and s take, each at most 33 for P-256. A type not defined here has none,
// and every signature is over it.
func SignatureBound(t string) int { return identityTypes[t].bound }

// withinBound refuses a signature longer than the bound of the identity key
// type t.
func withinBound(signature []byte, t string) error {
	if len(signature) > SignatureBound(t) {
		return errors.New("a signature is longer than the most a signature may be for its identity's key type")
	}
	return nil
}

// Sign signs message in namespace and returns the signature armored
// byte-for-byte as "ssh-keygen -Y sign" writes it.
func Sign(s ssh.Signer, message []byte, namespace string) ([]byte, error) {
	blob, err := sshsig.Sign(s, rand.Reader, message, namespace)
	if err != nil {
		return nil, err
	}
	return Armor(blob), nil
}

// Verify verifies an armored signature by id over message, in namespace.
func Verify(id *Identity, message, signature []byte, namespace string) error {
	if err := withinBound(signature, id.Type()); err != nil {
		return err
	}
	blob, err := Dearmor(signature)
	if err != nil {
		return err
	}
	return sshsig.Verify(id.PublicKey(), message, blob, namespace)
}

// Armor armors an SSHSIG blob as ssh-keygen does: base64 in lines of 70
// characters between the BEGIN and END lines, ending in LF.
func Armor(blob []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(blob)
	var b bytes.Buffer
	b.WriteString(armorBegin)
	for len(enc) > armorWidth {
		b.WriteString(enc[:armorWidth])
		b.WriteByte('\n')
		enc = enc[armorWidth:]
	}
	b.WriteString(enc)
	b.WriteString("\n" + armorEnd + "\n")
	return b.Bytes()
}

// Dearmor returns the SSHSIG blob of an armored signature of at most
// MaxSignature bytes in its one form, the bytes "ssh-keygen -Y sign" and Sign
// write (PROTOCOL.md §3.2). It parses the signature, writes it back in that
// form (oneForm, then Armor) and refuses any difference, as ParseIdentity
// does a document: so the armor is the BEGIN line, the base64 in lines of
// armorWidth, LF and the END line and its LF, nothing before or after; the
// blob is version 1, its signing key in its one encoding, an empty reserved
// field, sha512, and its signature with nothing after it, an ECDSA one's r
// and s each the minimal mpint. ECDSA's other s, n − s, is a signature of its
// own in the one form, not another form of one: both verify.
func Dearmor(armored []byte) ([]byte, error) {
	if len(armored) > MaxSignature {
		return nil, errors.New("a signature is longer than the most a signature may be")
	}
	text, ok := bytes.CutPrefix(armored, []byte(armorBegin))
	if !ok {
		return nil, errors.New("a signature starts with " + armorBegin[:len(armorBegin)-1])
	}
	text, ok = bytes.CutSuffix(text, []byte("\n"+armorEnd+"\n"))
	if !ok {
		return nil, errors.New("a signature ends with " + armorEnd + " and a line feed")
	}
	blob, err := base64.StdEncoding.DecodeString(string(bytes.ReplaceAll(text, []byte("\n"), nil)))
	if err != nil {
		return nil, errors.New("the signature is not valid base64")
	}
	one, err := oneForm(blob)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(Armor(one), armored) {
		return nil, errors.New("the signature is not in its one form")
	}
	return one, nil
}

// oneForm is an SSHSIG blob written in the one form (PROTOCOL.md §3.2) from
// what it parses as: "SSHSIG", version 1, the signing key as it is written
// back once parsed, with a certificate's own signature in its one form
// (oneSignature), the namespace, an empty reserved field, "sha512", and the
// signature in its one form. What does not parse so has none. The version,
// the reserved field, the hash's name and anything after the five fields are
// not read: the one form writes its own, and Dearmor refuses any difference.
func oneForm(blob []byte) ([]byte, error) {
	bad := errors.New("the signature is not an SSH signature")
	r := wireReader(blob)
	magic, ok := r.take(6)
	if !ok || string(magic) != "SSHSIG" {
		return nil, bad
	}
	_, ok0 := r.uint32()
	key, ok1 := r.string()
	namespace, ok2 := r.string()
	_, ok3 := r.string()
	_, ok4 := r.string()
	sig, ok5 := r.string()
	if !ok0 || !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return nil, bad
	}
	k, err := ssh.ParsePublicKey(key)
	if err != nil {
		return nil, errors.New("the signature's key does not parse")
	}
	if cert, ok := k.(*ssh.Certificate); ok && cert.Signature != nil {
		one := *cert
		one.Signature = oneSignature(cert.Signature)
		k = &one
	}
	s := new(ssh.Signature)
	if err := ssh.Unmarshal(sig, s); err != nil {
		return nil, bad
	}
	out := append([]byte("SSHSIG"), 0, 0, 0, 1)
	for _, f := range [][]byte{k.Marshal(), namespace, nil, []byte("sha512"), ssh.Marshal(oneSignature(s))} {
		out = appendWireString(out, f)
	}
	return out, nil
}

// oneSignature is an SSH signature in its one form: its type and its bytes,
// nothing after them, and an ECDSA signature's r and s each written as the
// minimal mpint, with no byte before it but the one zero a set high bit
// needs (RFC 4251 §5). An ECDSA signature whose numbers do not parse is left
// for the verifier to refuse.
func oneSignature(s *ssh.Signature) *ssh.Signature {
	one := &ssh.Signature{Format: s.Format, Blob: s.Blob}
	if strings.HasPrefix(s.Format, "ecdsa-sha2-") {
		var rs struct{ R, S *big.Int }
		if ssh.Unmarshal(s.Blob, &rs) == nil {
			one.Blob = ssh.Marshal(rs)
		}
	}
	return one
}

// lowS is an SSH signature with an ECDSA signature's s written as the lower
// of s and n − s, which both verify (PROTOCOL.md §3.2): one signature for the
// two a P-256 key's holder, or anyone who has seen one, can write. Any other
// signature is as it was.
func lowS(s *ssh.Signature) *ssh.Signature {
	if s.Format != ssh.KeyAlgoECDSA256 {
		return s
	}
	var rs struct{ R, S *big.Int }
	if ssh.Unmarshal(s.Blob, &rs) != nil {
		return s
	}
	n := elliptic.P256().Params().N
	if other := new(big.Int).Sub(n, rs.S); other.Sign() > 0 && other.Cmp(rs.S) < 0 {
		rs.S = other
	}
	return &ssh.Signature{Format: s.Format, Blob: ssh.Marshal(rs)}
}

// wireReader reads SSH wire form: RFC 4251's uint32 and string.
type wireReader []byte

func (r *wireReader) take(n int) ([]byte, bool) {
	if n < 0 || len(*r) < n {
		return nil, false
	}
	v := (*r)[:n]
	*r = (*r)[n:]
	return v, true
}

func (r *wireReader) uint32() (uint32, bool) {
	v, ok := r.take(4)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint32(v), true
}

func (r *wireReader) string() ([]byte, bool) {
	n, ok := r.uint32()
	if !ok || uint64(n) > uint64(len(*r)) {
		return nil, false
	}
	return r.take(int(n))
}

// appendWireString appends an RFC 4251 string. Every string here came out of
// a signature of at most MaxSignature bytes, so its length fits.
func appendWireString(b, s []byte) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s))) //nolint:gosec // bounded by MaxSignature
	return append(b, s...)
}
