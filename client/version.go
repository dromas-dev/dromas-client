package client

import (
	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/semver"

	"github.com/dromas-dev/dromas-client/protocol"
)

// Module is this client's module path: the version of it a binary was built
// with is the release it names ([Version]).
const Module = "github.com/dromas-dev/dromas-client"

// DevelVersion is the version a build that records none names: a build of
// this module's own checkout, Go's "(devel)", or one with no build
// information. A pre-release of 0.0.0, it is below every release.
const DevelVersion = "0.0.0-devel"

// LinkedVersion is the release a build of this module's source names where
// its build information records none, as a package manager's build from a
// release's source archive does: it is set at link time, as age's
// main.Version is (go build -ldflags "-X
// github.com/dromas-dev/dromas-client/client.LinkedVersion=v0.5.3"
// ./cmd/dromas). It looks like "v1.2.3". Where it is set, it is the version
// [Version] reads, and the version the dromas command prints.
var LinkedVersion string

// Version is the release this binary names in every request's User-Agent:
// the version of Module in its build information (runtime/debug), the
// binary's own module's where Module is the one built (the dromas command),
// or the one it depends on otherwise (a program that imports this library),
// without its "v" and without build metadata (Go's "+dirty",
// "+incompatible"). A build that records none of these is DevelVersion.
// [LinkedVersion], where it is set, comes before all of them. Its major and
// minor are the version of the protocol it implements, which every request
// names in Dromas-Version (PROTOCOL.md §11.17): a Go pseudo-version, a
// pre-release of the patch after the release it follows, has that release's.
func Version() string {
	if LinkedVersion != "" {
		return asRelease(LinkedVersion)
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return DevelVersion
	}
	v := ""
	if bi.Main.Path == Module {
		v = bi.Main.Version
	} else {
		for _, d := range bi.Deps {
			if d.Path == Module {
				v = d.Version
				if d.Replace != nil {
					v = d.Replace.Version
				}
				break
			}
		}
	}
	return asRelease(v)
}

// asRelease is a Go module version as this client names it: its "v" and its
// build metadata taken off, or DevelVersion for one that is not a version
// with or without its "v" (semver.IsValid), "(devel)" and "" among them.
func asRelease(v string) string {
	if v = "v" + strings.TrimPrefix(v, "v"); !semver.IsValid(v) {
		return DevelVersion
	}
	return strings.TrimPrefix(strings.TrimSuffix(v, semver.Build(v)), "v")
}

// Versions is what the answers a command met said of this client's version
// (PROTOCOL.md §11.17): a deprecation of it, from the Deprecation an answer
// carried, a sunset of it, from the Sunset one carried, and an upgrade, from the latest version the server's document names, read
// at most once a day. Every Mailbox made with the same Options.Versions
// keeps what it met there, so whoever runs the command says it once at its
// end. A Mailbox made with none keeps its own ([Mailbox.Versions]). It is
// safe for concurrent use.
//
// A command that runs for days, a resident [Mailbox.Follow], keeps a daily
// cycle: each prekey renewal a Receive makes after the command's first read of
// the server's document begins it anew. The first answer after a renewal
// begins replaces the deprecation and the sunset kept, so one the server no
// longer sends is forgotten, and the command reads the server's document
// again, however recently it last did.
type Versions struct {
	mu         sync.Mutex
	own        string
	answered   bool // a server answered one of the command's calls
	deprecated time.Time
	sunset     time.Time
	upgrade    string
	read       bool // the command has read the server's document, or tried to
	anew       bool // a renewal began: the next answer replaces deprecated and sunset
	due        bool // a renewal began: the next read is made whenever the last was
}

// Own is the version this client names in every request.
func (v *Versions) Own() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.own
}

// Sunset is the date of the earliest Sunset an answer the command met
// carried, the date its server stops taking this client's version, and false
// where none carried one of its form.
func (v *Versions) Sunset() (time.Time, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.sunset, !v.sunset.IsZero()
}

// Deprecation is the date of the earliest Deprecation an answer the command
// met carried, from which its server deems this client's version deprecated,
// and false where none carried one of its form. A date to come says the
// version will be deprecated then (RFC 9745 §2.1).
func (v *Versions) Deprecation() (time.Time, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.deprecated, !v.deprecated.IsZero()
}

// Upgrade is the latest version the server's document names, where the
// command's last read of it succeeded and it is an upgrade for this client
// (its major and minor are after this client's), and false otherwise: the document is
// read at most once a day, and at each renewal of a resident command's cycle.
func (v *Versions) Upgrade() (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.upgrade, v.upgrade != ""
}

// answer records what an answer's header says of versions: that a server
// answered, its Deprecation, as a Structured Field Date, and its Sunset, as
// an HTTP-date, each where it is of that form (PROTOCOL.md §11.17); anything
// else is none, and nothing of it is kept. Of each, the earliest the command
// met since its cycle began (renewing) is kept.
func (v *Versions) answer(h http.Header) {
	dep, dok := protocol.ParseDeprecation(h.Get(protocol.DeprecationHeader))
	sun, sok := protocol.ParseSunset(h.Get(protocol.SunsetHeader))
	v.mu.Lock()
	defer v.mu.Unlock()
	v.answered = true
	if v.anew {
		v.deprecated, v.sunset, v.anew = time.Time{}, time.Time{}, false
	}
	if dok && (v.deprecated.IsZero() || dep.Before(v.deprecated)) {
		v.deprecated = dep
	}
	if sok && (v.sunset.IsZero() || sun.Before(v.sunset)) {
		v.sunset = sun
	}
}

// renewing begins a new cycle at a renewal of the prekey, where the command
// has ended a cycle before, so has read the server's document or tried to
// (Versions): the next answer replaces what earlier ones said, and the
// document is read again at the end of the call that renews (recordVersions).
// A command's first cycle is the command itself, so one that ends after one
// renewal, a bare recv, is as it would be without it.
func (v *Versions) renewing() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.read {
		v.anew, v.due, v.read = true, true, false
	}
}

// Versions is what this mailbox's commands met of its version: Options.Versions,
// where it was given one.
func (b *Mailbox) Versions() *Versions { return b.met }

// documentEvery is how often a mailbox reads its server's document: once a
// day at most (PROTOCOL.md §11.17).
const documentEvery = 24 * time.Hour

// The mailbox's own records of versions, in its state's mailbox bucket:
//
//	deprecation     "<version> <time>": the date the last command that had an answer met in Deprecation, and this client's version then; none where its answers carried none
//	sunset          "<version> <time>": the same, of Sunset
//	document-read   when the mailbox last read its server's document, a time
const (
	keyDeprecation  = "deprecation"
	keySunset       = "sunset"
	keyDocumentRead = "document-read"
)

// recordVersions is the end of every command that holds the mailbox's lock
// (lockedFinished): where the command's calls had an answer, it reads the
// server's document once its last read is a day old (readDocument), and
// keeps in the mailbox's state the deprecation and the sunset its answers
// carried, or none, so that status says them with no call. Nothing it meets fails the command:
// a read that fails is tried again by the next command that has an answer,
// and a fault of the mailbox's state leaves what it held.
func (b *Mailbox) recordVersions(ctx context.Context) {
	m := b.met
	m.mu.Lock()
	answered, tried := m.answered, m.read
	m.mu.Unlock()
	if !answered {
		return
	}
	if !tried && ctx.Err() == nil && b.ended == nil && b.dir != "" {
		b.readDocument(ctx)
	}
	// After the read, whose answer may carry either too.
	m.mu.Lock()
	dated := map[string]time.Time{keyDeprecation: m.deprecated, keySunset: m.sunset}
	m.mu.Unlock()
	_ = b.update(func(s store) error { //nolint:errcheck // a fault leaves what was held, which the next command writes again
		for key, at := range dated {
			value := []byte(nil)
			if !at.IsZero() {
				value = []byte(b.version + " " + at.UTC().Format(protocol.TimeLayout))
			}
			var err error
			switch {
			case string(s.get(bucketMailbox, key)) == string(value):
			case value == nil:
				err = s.del(bucketMailbox, key)
			default:
				err = s.put(bucketMailbox, key, value)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// readDocument reads the server's document, GET /v1/, where the mailbox last
// read it a day ago or more, or never, or at a time its clock has since gone
// back past, or where a renewal began a cycle since (Versions.renewing), and
// keeps when it read it. Where the latest version it names is
// an upgrade for this client, the command says so (Versions.Upgrade). It
// reads latest_version alone: every other member, of any form, is ignored
// (RFC 7493 §4.2). A mailbox that holds its own revocation asks nothing
// (finished). A read that fails, or whose latest_version is no string, is no
// read: the next command that has an answer tries again.
func (b *Mailbox) readDocument(ctx context.Context) {
	b.met.mu.Lock()
	b.met.read = true
	due := b.met.due
	b.met.due = false
	b.met.mu.Unlock()
	now := b.now().UTC()
	var last []byte
	if err := b.view(func(s store) error { last = s.get(bucketMailbox, keyDocumentRead); return nil }); err != nil {
		return
	}
	if t, err := protocol.ParseTime(string(last)); err == nil && !due && !now.Before(t) && now.Sub(t) < documentEvery {
		return
	}
	if b.finished() != nil {
		return
	}
	var d struct {
		LatestVersion string `json:"latest_version"`
	}
	if _, err := b.exchange(ctx, http.MethodGet, protocol.DocumentPath, nil, nil, &d); err != nil {
		return
	}
	if err := b.update(func(s store) error {
		return s.put(bucketMailbox, keyDocumentRead, []byte(now.Format(protocol.TimeLayout)))
	}); err != nil {
		return
	}
	b.met.mu.Lock()
	b.met.upgrade = ""
	if upgrade(d.LatestVersion, b.version) {
		b.met.upgrade = d.LatestVersion
	}
	b.met.mu.Unlock()
}

// upgrade reports whether latest, a server's latest_version, is an upgrade
// for a client of release own: its major and minor are after own's
// (PROTOCOL.md §11.17). A value of another form is none, and a client
// released before its server names that release has none.
func upgrade(latest, own string) bool {
	p, err := protocol.ParseVersion(latest)
	return err == nil && p.Compare(protocol.RequestVersion(own)) > 0
}

// versionsAttention is what status says of versions, from what the mailbox's
// last commands kept, with no call: an upcoming removal of this client's
// version, a sunset of it or a deprecation whose date has passed at the
// mailbox's clock, each with its date. What was kept of another version of
// this client, one since upgraded from, is not said.
func (b *Mailbox) versionsAttention() []string {
	var deprecation, sunset []byte
	if err := b.view(func(s store) error {
		deprecation, sunset = s.get(bucketMailbox, keyDeprecation), s.get(bucketMailbox, keySunset)
		return nil
	}); err != nil {
		return nil
	}
	dated := func(kept []byte) (time.Time, bool) {
		v, at, ok := strings.Cut(string(kept), " ")
		if !ok || v != b.version {
			return time.Time{}, false
		}
		t, err := protocol.ParseTime(at)
		return t, err == nil
	}
	var said []string
	if t, ok := dated(deprecation); ok && !b.now().Before(t) {
		said = append(said, DeprecationWords(b.version, t))
	}
	if t, ok := dated(sunset); ok {
		said = append(said, SunsetWords(b.version, t))
	}
	return said
}

// SunsetWords say, in this client's own words, that the mailbox's server
// stops taking this client's version, own, at a date (PROTOCOL.md §11.17).
func SunsetWords(own string, at time.Time) string {
	return "the server stops taking this client's version, " + own + ", from " + at.UTC().Format(protocol.TimeLayout) +
		", by the Sunset its last answer carried: upgrade this client, whose calls it refuses as client_version_unsupported from then on"
}

// DeprecationWords say, in this client's own words, that the server deems
// this client's version, own, deprecated from a date (RFC 9745 §2.1).
func DeprecationWords(own string, at time.Time) string {
	return "the server deems this client's version, " + own + ", deprecated from " + at.UTC().Format(protocol.TimeLayout) +
		", by the Deprecation its last answer carried: it still takes it, and an upgrade of this client is due"
}

// UpgradeWords say, in this client's own words, that the mailbox's server
// names a later version of this client than own.
func UpgradeWords(own, latest string) string {
	return "the server names " + latest + " as the latest version of this client, above this one, " + own + ": an upgrade is available"
}
