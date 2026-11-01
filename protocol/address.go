package protocol

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"strings"
)

// Address is a mailbox's address: the SHA-384 of its identity document. Its
// text form is AddressForm, "_", then those 48 bytes in base64url (RFC 4648
// §5) without padding: AddressLen characters, the only form clients may send.
// The text before the first "_" names the form, so an address never begins
// with "-", whatever its bytes; it is compared as an exact string, case and
// all.
type Address [sha512.Size384]byte

// AddressForm is the one form of address this version defines: the name
// before the first "_" of every address's text.
const AddressForm = "d"

// addressPrefix is what every address's text begins with.
const addressPrefix = AddressForm + "_"

// AddressLen is the length of an address's text form: the prefix, then 384
// bits in 6-bit characters, none of them unused.
const AddressLen = len(addressPrefix) + 8*sha512.Size384/6

// AddressOf returns the address of an identity document: its SHA-384.
func AddressOf(document []byte) Address { return sha512.Sum384(document) }

// LinkPath is where a server serves a page for each address: an address is
// shared as the link <server>/to/<address>, which tells whoever opens it
// what dromas is. A client takes the address from the link and never
// fetches the page.
const LinkPath = "/to/"

// errAddress is ParseAddress's one error, for any text not an address.
var errAddress = errors.New(`an address is "d_" and 64 characters of unpadded base64url (A-Z a-z 0-9 - _)`)

// ParseAddress parses the text form of an address: AddressForm and "_", then
// exactly 64 characters of unpadded base64url, the one encoding of 48 bytes.
// A form this version does not define is no address. The recipient compares
// addresses as exact strings, so a letter in the other case is another
// address, never one to fold.
func ParseAddress(s string) (Address, error) {
	var a Address
	rest, ok := strings.CutPrefix(s, addressPrefix)
	if !ok || len(s) != AddressLen {
		return a, errAddress
	}
	n, err := base64.RawURLEncoding.Strict().Decode(a[:], []byte(rest))
	if err != nil || n != len(a) {
		return Address{}, errAddress
	}
	return a, nil
}

// String returns the address's text form: AddressForm, "_", and 64
// characters of unpadded base64url.
func (a Address) String() string { return addressPrefix + base64.RawURLEncoding.EncodeToString(a[:]) }

// MarshalText implements [encoding.TextMarshaler].
func (a Address) MarshalText() ([]byte, error) { return []byte(a.String()), nil }

// UnmarshalText implements [encoding.TextUnmarshaler], with [ParseAddress]'s rules.
func (a *Address) UnmarshalText(b []byte) error {
	v, err := ParseAddress(string(b))
	if err != nil {
		return err
	}
	*a = v
	return nil
}
