package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"filippo.io/edwards25519"
	"golang.org/x/crypto/ssh"
)

const (
	// IdentityTag is line 1 of every v1 identity document.
	IdentityTag = "dromas-v1"
)

// The identity key types PROTOCOL.md §4.1 defines: the SSH name on line 2 of
// an identity document. Every client verifies both. A type defined after them
// is optional: a mailbox of one is reached only by clients that verify it,
// which declare it (§7).
const (
	IdentityEd25519 = ssh.KeyAlgoED25519
	IdentityP256    = ssh.KeyAlgoECDSA256
)

// IdentityTypes returns the identity key types this package verifies, those of
// PROTOCOL.md §4.1 first.
func IdentityTypes() []string { return slices.Clone(identityOrder) }

// identityOrder is IdentityTypes' list, each a type identityTypes defines.
var identityOrder = []string{IdentityEd25519, IdentityP256}

// OptionalTypes returns the identity key types this package verifies that
// PROTOCOL.md §4.1 does not define, in IdentityTypes' order: the declaration
// a mailbox makes with each prekey (§7). It is empty while every type this
// package verifies is one of §4.1's.
func OptionalTypes() []string {
	var o []string
	for _, t := range IdentityTypes() {
		if identityTypes[t].optional {
			o = append(o, t)
		}
	}
	return o
}

// Declaration returns what a server keeps of a mailbox's declaration of
// sender key types: the optional types of this package that it names, in
// OptionalTypes' order, and none of the rest, a type of §4.1 among them
// (PROTOCOL.md §7).
func Declaration(types []string) []string {
	var kept []string
	for _, t := range OptionalTypes() {
		if slices.Contains(types, t) {
			kept = append(kept, t)
		}
	}
	return kept
}

// AcceptsSender reports whether a mailbox that declared the sender key types
// declared takes messages from a mailbox whose identity is of type t, as far
// as the declaration says: a type of PROTOCOL.md §4.1 always, and an optional
// type only where it was declared (§7).
func AcceptsSender(declared []string, t string) bool {
	k, defined := identityTypes[t]
	return defined && !k.optional || slices.Contains(declared, t)
}

// Every ParseIdentity error wraps one of these: the document breaks its
// format, its key fails the key checks, or it is a well-formed document of a
// key type this package does not define. A server answers the first with
// identity_mismatch, the second with bad_key and the third with
// unsupported_key_type.
var (
	ErrIdentityFormat = errors.New("not an identity document")
	ErrIdentityKey    = errors.New("bad identity key")
	ErrIdentityType   = errors.New("an identity key type this client does not verify")
)

// identityForm is the form every identity document has, whatever its key
// type: the tag line, then an SSH key type's name, one space and base64, each
// line ending in LF, nothing after. It is what lets a client tell a document of
// a type it lacks from something that is no document. A name is as RFC 4251 §6
// writes one: printable ASCII with no comma, and either no at-sign or one,
// followed by a fully qualified domain name; at most maxKeyTypeName
// characters, which a pattern cannot count beside the domain's.
var identityForm = regexp.MustCompile(`\A` + IdentityTag + `\n([!-+\--?A-~]+(?:@` + domainLabel + `(?:\.` + domainLabel + `)+)?) ([A-Za-z0-9+/]+={0,2})\n\z`)

// domainLabel is a label of a domain name (RFC 1123 §2.1): letters, digits and
// hyphens, at most 63, neither first nor last a hyphen.
const (
	domainLabel    = `[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?`
	maxKeyTypeName = 64
)

// A defined type: the checks its key must pass beyond being a key of that
// type in its one encoding, the type of the working keys an identity of that
// type certifies (WorkingKeyType), and the most bytes a signature by a mailbox
// of that type may be (SignatureBound): the largest it makes in the one form. Its document's form is the one every
// type shares (identityForm), with the key in its one encoding: the base64 of
// the wire key as it is written back once parsed (PROTOCOL.md §4.1). Ed25519's
// is 91 bytes and P-256's 171. A working key's type is itself a type defined
// here, and its key is held to that type's checks.
var identityTypes = map[string]struct {
	check func(ssh.PublicKey) error
	work  string
	bound int
	// optional is a type PROTOCOL.md §4.1 does not define: one a mailbox
	// declares (§7).
	optional bool
}{
	// Its bound is exact: a prekey statement or a pass under a certificate,
	// each 850 bytes in the one form.
	IdentityEd25519: {
		check: func(k ssh.PublicKey) error {
			return checkEd25519(k.(ssh.CryptoPublicKey).CryptoPublicKey().(ed25519.PublicKey))
		},
		work:  IdentityEd25519,
		bound: 850,
	},
	// Its bound is a prekey statement under a certificate whose signature
	// by the identity has an r and an s of 33 bytes each, the most the one
	// form gives them: one that has fewer is shorter. Its key has no check of
	// its own: the library refuses a point off the curve, compressed, at
	// infinity or with a coordinate not below the field's size, and that is
	// all OpenSSH from 10.3p1 holds a point to.
	IdentityP256: {
		check: func(ssh.PublicKey) error { return nil },
		work:  IdentityEd25519,
		bound: 947,
	},
}

// WorkingKeyType returns the key type of every working key an identity of
// key type t certifies (PROTOCOL.md §4.1, §5), and "" for a type not defined
// here.
func WorkingKeyType(t string) string { return identityTypes[t].work }

// Identity is a mailbox's identity: its identity document, the key in it, and the
// address that document hashes to.
type Identity struct {
	doc  []byte
	key  ssh.PublicKey
	addr Address
}

// ParseIdentity parses an identity document. It must be the two-line form
// every type shares, with the key in its one encoding, the wire key as it is
// written back once parsed, and the key must pass that type's checks: for
// Ed25519 a canonical point that is not of small order, which Go's
// crypto/ed25519 accepts, as OpenSSH's own code did before 10.6p1, so the
// check is explicit; for P-256 a point on the curve, which the library
// checks as it reads the key, as OpenSSH from 10.3p1 does.
//
// A well-formed document of a type this package does not define is
// ErrIdentityType: nothing is wrong with it, and nothing it signed can be
// checked here.
func ParseIdentity(doc []byte) (*Identity, error) {
	m := identityForm.FindSubmatch(doc)
	if m == nil || len(m[1]) > maxKeyTypeName {
		return nil, fmt.Errorf("%w: it is \"dromas-v1\", LF, one SSH public key with no comment, LF (look for a comment, CRLF, or a missing final newline)", ErrIdentityFormat)
	}
	name, b64 := string(m[1]), string(m[2])
	wire, err := base64.StdEncoding.Strict().DecodeString(b64)
	t, defined := identityTypes[name]
	if !defined {
		// A document at all? Its key is base64, in its one encoding, of an
		// SSH wire key, which begins with its own type's name.
		if err != nil || !bytes.HasPrefix(wire, sshString(name)) {
			return nil, fmt.Errorf("%w: line 2 is a key type's name, a space, and that key in base64", ErrIdentityFormat)
		}
		return nil, ErrIdentityType
	}
	// The key in its one encoding: strict base64 of a wire key of the type it
	// names, which parses, and is written back as the same bytes.
	if err != nil {
		return nil, fmt.Errorf("%w: line 2's base64 is not in its one encoding", ErrIdentityFormat)
	}
	key, err := ssh.ParsePublicKey(wire)
	if err != nil || key.Type() != name || !bytes.Equal(key.Marshal(), wire) {
		return nil, fmt.Errorf("%w: line 2 is not a key of the type it names, %s, in that type's one encoding", ErrIdentityFormat, name)
	}
	if err := t.check(key); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentityKey, err)
	}
	return &Identity{
		doc:  bytes.Clone(doc),
		key:  key,
		addr: AddressOf(doc),
	}, nil
}

// NewIdentity builds the identity document for an Ed25519 public key and
// parses it, with every check ParseIdentity makes.
func NewIdentity(pub ed25519.PublicKey) (*Identity, error) {
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return IdentityOf(key)
}

// IdentityOf builds the identity document for a public key of any type this
// package defines and parses it, with every check ParseIdentity makes.
func IdentityOf(key ssh.PublicKey) (*Identity, error) {
	doc := []byte(IdentityTag + "\n" + key.Type() + " " + base64.StdEncoding.EncodeToString(key.Marshal()) + "\n")
	return ParseIdentity(doc)
}

// Document returns the identity document, exactly as hashed.
func (id *Identity) Document() []byte { return bytes.Clone(id.doc) }

// Address returns the address: the SHA-384 of the identity document.
func (id *Identity) Address() Address { return id.addr }

// PublicKey returns the identity key, for verifying its signatures.
func (id *Identity) PublicKey() ssh.PublicKey { return id.key }

// Type returns the identity's key type: the name on line 2 of its document.
func (id *Identity) Type() string { return id.key.Type() }

// sshString is s as an SSH wire string: its length in four bytes, then it.
func sshString(s string) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(s))), s...) //nolint:gosec // a key type's name: at most 64 bytes
}

// checkEd25519 rejects a key that is not a canonical encoding of a curve
// point, or that is of small order, as libsodium does. A small-order key can
// make one signature verify for many messages.
func checkEd25519(k ed25519.PublicKey) error {
	if len(k) != ed25519.PublicKeySize {
		return errors.New("an Ed25519 key is 32 bytes")
	}
	p, err := new(edwards25519.Point).SetBytes(k)
	if err != nil {
		return errors.New("the identity key is not a point on the curve")
	}
	// SetBytes accepts non-canonical encodings; a canonical one re-encodes
	// to the same bytes.
	if !bytes.Equal(p.Bytes(), k) {
		return errors.New("the identity key is not canonically encoded")
	}
	if new(edwards25519.Point).MultByCofactor(p).Equal(edwards25519.NewIdentityPoint()) == 1 {
		return errors.New("the identity key is of small order")
	}
	return nil
}
