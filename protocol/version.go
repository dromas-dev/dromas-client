package protocol

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"
)

// The header fields of versions (PROTOCOL.md §11.17): every request names the
// version of the protocol its client implements in VersionHeader, an answer
// to a version a deprecation covers carries DeprecationHeader (RFC 9745), and
// one to a version a sunset covers SunsetHeader (RFC 8594).
const (
	VersionHeader     = "Dromas-Version"
	DeprecationHeader = "Deprecation"
	SunsetHeader      = "Sunset"
)

// DocumentPath is the server's document, GET /v1/ (PROTOCOL.md §11.18).
const DocumentPath = "/v1/"

// Version is a version of the protocol, MAJOR.MINOR (PROTOCOL.md §11.17):
// the version every request names in Dromas-Version, the one its client
// implements, and each version a server's policy names.
type Version struct {
	Major, Minor uint64
}

// versionGrammar is a version: two numbers with no leading zero
// (PROTOCOL.md §11.17), then the value's end or a character that is not a
// digit. A request's Dromas-Version is read by its leading version, the rest
// ignored ([RequestVersion]); everything else is held to it whole.
var versionGrammar = regexp.MustCompile(`\A(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:[^0-9]|\z)`)

// errVersion is a value that is not a version. Its words never quote the
// value, which may be a server's or a caller's.
var errVersion = errors.New("not a version of its form")

// ParseVersion reads s as a version, MAJOR.MINOR, exactly. Anything else, a
// "v" before it or a patch after it included, or a number past a uint64, is
// an error.
func ParseVersion(s string) (Version, error) {
	v, n, err := leadingVersion(s)
	if err != nil || n != len(s) {
		return Version{}, errVersion
	}
	return v, nil
}

// RequestVersion is the version a server reads a request's Dromas-Version
// as: the major and minor it begins with, ended by the value's end or a
// character that is not a digit, whatever follows them, so 0.6, 0.6.11 and
// 0.6.11-rc.1 read alike; or 0.0, for a request without the field or with a
// value that does not begin with a version, 1.05 among them (PROTOCOL.md
// §11.17). A
// value given twice is the caller's to join or refuse: this reads one.
func RequestVersion(s string) Version {
	v, _, err := leadingVersion(s)
	if err != nil {
		return Version{}
	}
	return v
}

// leadingVersion is the version s begins with, and its length.
func leadingVersion(s string) (Version, int, error) {
	m := versionGrammar.FindStringSubmatch(s)
	if m == nil {
		return Version{}, 0, errVersion
	}
	var v Version
	var err error
	if v.Major, err = strconv.ParseUint(m[1], 10, 64); err != nil {
		return Version{}, 0, errVersion
	}
	if v.Minor, err = strconv.ParseUint(m[2], 10, 64); err != nil {
		return Version{}, 0, errVersion
	}
	return v, len(m[1]) + 1 + len(m[2]), nil
}

// String writes v as a request and a policy write it.
func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major, v.Minor) }

// Compare orders v and w by major, then minor: -1 when v is before w, 0 when
// they are the same, 1 when v is after.
func (v Version) Compare(w Version) int {
	return cmp.Or(cmp.Compare(v.Major, w.Major), cmp.Compare(v.Minor, w.Minor))
}

// The most deprecations, sunsets and capabilities a server's document lists,
// and a reader reads (PROTOCOL.md §11.18): past any, a document breaks its
// form.
const (
	MaxDeprecations = 64
	MaxSunsets      = 64
	MaxCapabilities = 64
)

// capabilityGrammar is a capability's name: 1 to 64 of a–z, 0–9 and _,
// beginning with a letter (PROTOCOL.md §11.18).
var capabilityGrammar = regexp.MustCompile(`\A[a-z][a-z0-9_]{0,63}\z`)

// ServerDocument is GET /v1/, the server's version policy and the
// capabilities it serves (PROTOCOL.md §11.18). Its versions and times are
// strings, as they travel, each held to its form by [ServerDocument.Check],
// which a read does not make: a client reads only the members it uses. A
// writer writes both lists, empty or not.
type ServerDocument struct {
	// MinVersion is the floor, "" for none.
	MinVersion string `json:"min_version,omitempty"`
	// LatestVersion is the latest version the server names, "" for none.
	LatestVersion string       `json:"latest_version,omitempty"`
	Deprecations  []Scheduled  `json:"deprecations"`
	Sunsets       []Scheduled  `json:"sunsets"`
	Capabilities  []Capability `json:"capabilities"`
	// MaxCertificateLifetime is the longest a certificate the server takes may
	// run, valid-before less valid-after, in seconds (PROTOCOL.md §11.7): from
	// 1 to MaxInteger.
	MaxCertificateLifetime uint64 `json:"max_certificate_lifetime_seconds"`
	// Server is the implementation that answers, nil where the server names
	// none: informational ([Implementation]).
	Server *Implementation `json:"server,omitempty"`
}

// Implementation is what a server says of the software that answers
// (PROTOCOL.md §11.18): its name, its version and the source revision it
// was built from, for a reader to check against that implementation's
// releases. It is informational. A client never speaks the protocol
// otherwise on it, and nothing a client checks rests on it: a server may
// name anything.
type Implementation struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"` // "" where the build records none
}

// implementationText is each member of an Implementation: 1 to 128
// characters of %x21-7E (PROTOCOL.md §11.18).
var implementationText = regexp.MustCompile(`\A[\x21-\x7e]{1,128}\z`)

// Scheduled is a deprecation or a sunset of versions, and its date: a
// deprecation, a date from which the server deems them deprecated (RFC
// 9745), and a sunset, the date it stops taking them (RFC 8594). It covers
// every version before Below, or the version Version names; exactly one of
// the two is set. At is its date, a time (PROTOCOL.md §2.2).
type Scheduled struct {
	Below   string `json:"below,omitempty"`
	Version string `json:"version,omitempty"`
	At      string `json:"effective_at"`
}

// Capability is one capability of version 1 the server serves, by its name.
type Capability struct {
	Name string `json:"name"`
}

// Check holds d to its form: each version of its form, each deprecation and
// sunset with exactly one of below and version and its date a time, each
// capability's name of its form, the implementation's members of theirs,
// and no more than MaxDeprecations,
// MaxSunsets and MaxCapabilities of each; and to RFC 9745 §4's rule, that no
// answer's Sunset is earlier than its Deprecation, for every version
// (PROTOCOL.md §11.17). It names the member that breaks it ([Member]).
func (d *ServerDocument) Check() error {
	for name, v := range map[string]string{"min_version": d.MinVersion, "latest_version": d.LatestVersion} {
		if v != "" {
			if _, err := ParseVersion(v); err != nil {
				return &memberError{name: name, known: true}
			}
		}
	}
	if len(d.Deprecations) > MaxDeprecations {
		return &memberError{name: "deprecations", known: true}
	}
	if len(d.Sunsets) > MaxSunsets {
		return &memberError{name: "sunsets", known: true}
	}
	for _, s := range append(slices.Clone(d.Deprecations), d.Sunsets...) {
		if _, _, err := s.parse(); err != nil {
			return err
		}
	}
	// What covers a version changes only at a version the policy names, so
	// each such version and the one just below it stand for every version.
	for _, s := range append(slices.Clone(d.Deprecations), d.Sunsets...) {
		p, _, _ := s.parse()
		at := []Version{p}
		switch {
		case p.Minor > 0:
			at = append(at, Version{Major: p.Major, Minor: p.Minor - 1})
		case p.Major > 0:
			at = append(at, Version{Major: p.Major - 1, Minor: math.MaxUint64})
		}
		for _, v := range at {
			from, deprecated := d.DeprecationFor(v)
			end, sunset := d.SunsetFor(v)
			if deprecated && sunset && end.Before(from) {
				return &memberError{name: "sunsets", known: true}
			}
		}
	}
	if len(d.Capabilities) > MaxCapabilities {
		return &memberError{name: "capabilities", known: true}
	}
	if d.MaxCertificateLifetime == 0 || d.MaxCertificateLifetime > MaxInteger {
		return &memberError{name: "max_certificate_lifetime_seconds", known: true}
	}
	for _, c := range d.Capabilities {
		if !capabilityGrammar.MatchString(c.Name) {
			return &memberError{name: "name", known: true}
		}
	}
	if im := d.Server; im != nil {
		for name, v := range map[string]string{"name": im.Name, "version": im.Version} {
			if !implementationText.MatchString(v) {
				return &memberError{name: name, known: true}
			}
		}
		if im.Revision != "" && !implementationText.MatchString(im.Revision) {
			return &memberError{name: "revision", known: true}
		}
	}
	return nil
}

// parse is s's version, as below or as version, and its date, or the member
// that breaks its form.
func (s Scheduled) parse() (Version, time.Time, error) {
	if (s.Below == "") == (s.Version == "") {
		return Version{}, time.Time{}, &memberError{name: "below", known: true}
	}
	name, v := "below", s.Below
	if s.Version != "" {
		name, v = "version", s.Version
	}
	p, err := ParseVersion(v)
	if err != nil {
		return Version{}, time.Time{}, &memberError{name: name, known: true}
	}
	at, err := ParseTime(s.At)
	if err != nil {
		return Version{}, time.Time{}, &memberError{name: "effective_at", known: true}
	}
	return p, at, nil
}

// Covers reports whether s covers v (PROTOCOL.md §11.17). One not of its
// form covers nothing.
func (s Scheduled) Covers(v Version) bool {
	p, _, err := s.parse()
	switch {
	case err != nil:
		return false
	case s.Below != "":
		return v.Compare(p) < 0
	}
	return v == p
}

// SunsetFor is the date an answer to a request of version v carries in
// Sunset: the earliest of the sunsets that cover it, and false where none
// does (PROTOCOL.md §11.17).
func (d *ServerDocument) SunsetFor(v Version) (time.Time, bool) { return earliest(d.Sunsets, v) }

// DeprecationFor is the date an answer to a request of version v carries in
// Deprecation: the earliest of the deprecations that cover it, and false
// where none does (PROTOCOL.md §11.17).
func (d *ServerDocument) DeprecationFor(v Version) (time.Time, bool) {
	return earliest(d.Deprecations, v)
}

// earliest is the earliest date of those of list that cover v.
func earliest(list []Scheduled, v Version) (time.Time, bool) {
	var first time.Time
	found := false
	for _, s := range list {
		_, at, err := s.parse()
		if err != nil || !s.Covers(v) {
			continue
		}
		if !found || at.Before(first) {
			first, found = at, true
		}
	}
	return first, found
}

// Retired reports whether a request of version v is answered 410
// client_version_unsupported at the server's clock now: v is below the floor, or a
// sunset that covers it has its date at or before now (PROTOCOL.md §11.17).
func (d *ServerDocument) Retired(v Version, now time.Time) bool {
	if d.MinVersion != "" {
		if p, err := ParseVersion(d.MinVersion); err == nil && v.Compare(p) < 0 {
			return true
		}
	}
	at, ok := d.SunsetFor(v)
	return ok && !now.Before(at)
}

// FormatSunset writes t as Sunset carries it, an HTTP-date in its preferred
// form, IMF-fixdate (RFC 8594 §3, RFC 9110 §5.6.7).
func FormatSunset(t time.Time) string { return t.UTC().Format(http.TimeFormat) }

// FormatDeprecation writes t as Deprecation carries it, a Structured Field
// Date, "@" and the Unix time in seconds (RFC 9745 §2.1, RFC 9651 §3.3.7).
func FormatDeprecation(t time.Time) string { return "@" + strconv.FormatInt(t.Unix(), 10) }

// deprecationGrammar is a Deprecation's value: an Item that is a Date, "@"
// and an Integer, an optional "-" and 1 to 15 digits (RFC 9651 §3.3.1,
// §3.3.7), with no parameters.
var deprecationGrammar = regexp.MustCompile(`\A@-?[0-9]{1,15}\z`)

// ParseDeprecation reads a Deprecation's value as RFC 9745 §2.1 writes it,
// a Date, exactly, and false for a value of any other form, which is none
// (PROTOCOL.md §11.17).
func ParseDeprecation(v string) (time.Time, bool) {
	if !deprecationGrammar.MatchString(v) {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(v[1:], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

// ParseSunset reads a Sunset's value as an HTTP-date, in any of the three
// forms RFC 9110 §5.6.7 has a recipient take (http.ParseTime), and false for
// a value of any other form, which is none (PROTOCOL.md §11.17).
func ParseSunset(v string) (time.Time, bool) {
	if v == "" {
		return time.Time{}, false
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}
