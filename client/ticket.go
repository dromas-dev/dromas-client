package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"net/http/httptrace"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"
)

// ticketFile is the file in the rotating keys' directory that keeps a TLS
// session ticket (client/doc.go).
const ticketFile = "tls-ticket"

// extraBase, extraTicket and extraFull name what ticketCache adds to a
// session's tls.SessionState.Extra, the place crypto/tls gives a session cache
// for what it stores beside a session: the server's URL, the ticket itself, and
// the time of the last full handshake to that server, in Unix seconds.
const (
	extraBase   = "dromas-base "
	extraTicket = "dromas-ticket "
	extraFull   = "dromas-full "
)

// resumeWithin is how long after the last full handshake to the mailbox's
// server the client resumes a session: past it, Get offers none, and the next
// connection makes a full handshake. RFC 8446 §4.6.1 RECOMMENDS a limit on the
// total lifetime of resumption keying material, which takes into account "the
// time since the peer's online CertificateVerify signature". This one counts
// from the last full handshake that completed, the last time the server proved
// its certificate's key, and a resumption never moves it.
const resumeWithin = 24 * time.Hour

// ticketCache is the default client's TLS session cache. The embedded cache
// holds what this process learned, of every server name. The file holds the
// last TLS 1.3 ticket of the mailbox's server, so a new process resumes TLS on
// its first connection (RFC 8446 §2.2). A written ticket authenticates the
// server it resumes to, because a resumed session carries no certificate, and it
// authenticates the mailbox to nobody, so every call that needs a pass still
// carries one (PROTOCOL.md §8). A session read from the file offers no early
// data (decode); no test holds that, because crypto/tls offers early data only
// over QUIC. Past resumeWithin, the outer limit is crypto/tls's own: it resumes
// no session past its leaf certificate's NotAfter, or whose verified chain no
// longer verifies. Whoever can write the rotating keys' directory can plant a
// ticket, which is within that directory's trust.
//
// The ticket's secret is a key: whoever reads the file and is on the network
// path can stand in for the server. The state file holds no key,
// because a key deleted inside a database stays in its free pages
// (client/store.go). crypto/tls calls Put as a connection reads, during a Hold
// that holds no lock, and during a command that holds the state file. So Put
// writes the ticket's own file, through writeKey, with no lock and no
// transaction. Two processes that write at once leave one whole ticket, and
// either ticket resumes.
//
// Only a TLS 1.3 ticket goes in the file. A TLS 1.2 session's secret is its master
// secret, which decrypts that session's recorded traffic. A TLS 1.3 ticket's
// secret does not, and crypto/tls resumes TLS 1.3 with a new key agreement
// (psk_dhe_ke).
//
// Whoever reads the file stands in for the server for at most resumeWithin
// after the last full handshake: every connection that resumes, the tickets
// that party issues included, keeps that time, and only a full handshake that
// completed moves it (handshakeDone). The file keeps the time beside the
// ticket, so a new process judges by it. A file that keeps no time, or a time
// that does not parse, gives the process no time: a process that knows no time
// of its own takes no session from the file, and the next full handshake writes
// a time. The time is the mailbox's
// clock's, and one ahead of the clock gives no session. Every connection of a
// process shares the cache, a Hold's beside a command's, and the time is
// under the cache's lock. A process writes the latest time it knows of, so a
// process that knows an earlier one than another shortens the other's bound,
// which costs a full handshake.
//
// A ticket is only an optimisation: a read or a write that fails costs a full
// handshake, fails no call, and is said nowhere.
type ticketCache struct {
	tls.ClientSessionCache
	file string           // where the ticket is kept
	base string           // the server URL the mailbox calls
	name string           // the key crypto/tls gives that server's sessions
	now  func() time.Time // the mailbox's clock

	mu   sync.Mutex
	full time.Time // the last full handshake to the server this process knows of, or zero
}

// newTicketCache is a ticketCache that keeps sessions of base, a URL
// checkServerURL took, in file. crypto/tls keys a session by tls.Config.ServerName,
// which net/http sets to the URL's host without its port. So a proxy that speaks
// TLS has another key, and goes no further than memory. Two ports of one host
// share a key, so the file names the whole URL too. now is the mailbox's clock.
func newTicketCache(memory tls.ClientSessionCache, file, base string, now func() time.Time) *ticketCache {
	c := &ticketCache{ClientSessionCache: memory, file: file, base: base, now: now}
	if u, err := url.Parse(base); err == nil {
		c.name = u.Hostname() // an IPv6 host without its brackets, as crypto/tls keys it
	}
	return c
}

// Get is the session this process holds for key, or the ticket in the file where
// key is the server's and the process holds none. Of the server's, it gives one
// only within resumeWithin of the last full handshake (fresh), and it removes a
// file whose time has passed.
func (c *ticketCache) Get(key string) (*tls.ClientSessionState, bool) {
	cs, ok := c.ClientSessionCache.Get(key)
	if key != c.name {
		return cs, ok
	}
	if ok && cs != nil && c.fresh() {
		return cs, true
	}
	if cs = c.read(); cs == nil {
		return nil, false
	}
	if !c.fresh() {
		c.drop()
		return nil, false
	}
	return cs, true
}

// fresh reports whether the mailbox's clock is within resumeWithin after the last
// full handshake this process knows of. With none known, or one ahead of the
// clock, it is not.
func (c *ticketCache) fresh() bool {
	full := c.lastFull()
	age := c.now().Sub(full)
	return !full.IsZero() && age >= 0 && age <= resumeWithin
}

func (c *ticketCache) lastFull() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.full
}

// learn takes t, a time the file keeps, where it is later than what the process
// knows: another process made the full handshake.
func (c *ticketCache) learn(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.full) {
		c.full = t
	}
}

// trace is ctx with handshakeDone called for each TLS handshake a call of the
// mailbox's makes: net/http calls httptrace's TLSHandshakeDone once a handshake
// has returned, with its error.
func (c *ticketCache) trace(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{TLSHandshakeDone: c.handshakeDone})
}

// handshakeDone records a full handshake to the mailbox's server that completed,
// at the mailbox's clock: one with no error, not resumed, with a chain crypto/tls
// verified for a certificate that names the server. A completed handshake is
// one whose server signed it with its certificate's key (RFC 8446 §4.4.3) and
// sent its Finished. tls.Config.VerifyConnection is not that: crypto/tls calls
// it once the certificate's chain verifies, before it checks the server's
// CertificateVerify, so a party holding only the server's certificate reaches
// it. A connection to another name, a proxy's, records nothing.
func (c *ticketCache) handshakeDone(cs tls.ConnectionState, err error) {
	if err != nil || cs.DidResume || len(cs.VerifiedChains) == 0 || len(cs.PeerCertificates) == 0 ||
		cs.PeerCertificates[0].VerifyHostname(c.name) != nil {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.full = now
}

// Put holds cs for key, and keeps it in the file where key is the server's. A nil
// cs, which crypto/tls gives for a session no longer of use, or one that the file
// does not take, removes the file.
func (c *ticketCache) Put(key string, cs *tls.ClientSessionState) {
	c.ClientSessionCache.Put(key, cs)
	if key != c.name {
		return
	}
	raw := c.encode(cs)
	if raw == nil {
		c.drop()
		return
	}
	_ = writeKey(c.file, raw) // a ticket is only an optimisation
}

// encode is what the file holds of cs, with the last full handshake this process
// knows of, or nil where the file does not take cs: no session, or one of a TLS
// version other than 1.3, which SessionState.Bytes writes first (the
// SessionState encoding, crypto/tls/ticket.go).
func (c *ticketCache) encode(cs *tls.ClientSessionState) []byte {
	if cs == nil {
		return nil
	}
	ticket, state, err := cs.ResumptionState()
	if err != nil || state == nil {
		return nil
	}
	kept := *state
	kept.Extra = append(slices.Clip(state.Extra), []byte(extraBase+c.base), append([]byte(extraTicket), ticket...))
	if full := c.lastFull(); !full.IsZero() {
		kept.Extra = append(kept.Extra, []byte(extraFull+strconv.FormatInt(full.Unix(), 10)))
	}
	raw, err := kept.Bytes()
	if err != nil || len(raw) < 2 || binary.BigEndian.Uint16(raw) != tls.VersionTLS13 {
		return nil
	}
	return raw
}

// read is the session in the file. A file that others can reach, that another
// user owns (checkPrivateFile), that cannot be read, or that holds no ticket of
// this server URL gives none, and is removed, so a changed URL drops the ticket.
// A written ticket authenticates the server it resumes to, so its writer is
// judged as a private file's is.
func (c *ticketCache) read() *tls.ClientSessionState {
	if err := checkPrivateFile(c.file); err != nil {
		c.drop()
		return nil
	}
	raw, err := os.ReadFile(c.file)
	if err != nil {
		c.drop()
		return nil
	}
	cs := c.decode(raw)
	if cs == nil {
		c.drop()
	}
	return cs
}

// decode is the session in raw, or nil where raw holds no ticket of this server URL.
// Each entry a later encode added comes after any earlier one, so the last counts.
// The time of the last full handshake that raw keeps is learned (learn); raw
// that keeps none, or one that does not parse, teaches nothing.
func (c *ticketCache) decode(raw []byte) *tls.ClientSessionState {
	state, err := tls.ParseSessionState(raw)
	if err != nil {
		return nil
	}
	var base, ticket, full []byte
	for _, e := range state.Extra {
		if v, ok := bytes.CutPrefix(e, []byte(extraBase)); ok {
			base = v
		}
		if v, ok := bytes.CutPrefix(e, []byte(extraTicket)); ok {
			ticket = v
		}
		if v, ok := bytes.CutPrefix(e, []byte(extraFull)); ok {
			full = v
		}
	}
	if string(base) != c.base || len(ticket) == 0 {
		return nil
	}
	if secs, err := strconv.ParseInt(string(full), 10, 64); err == nil {
		c.learn(time.Unix(secs, 0))
	}
	state.EarlyData = false // no 0-RTT, as SessionState.EarlyData offers an application
	cs, err := tls.NewResumptionState(ticket, state)
	if err != nil {
		return nil
	}
	return cs
}

// drop removes the file. A failure leaves a ticket that the next read judges again.
func (c *ticketCache) drop() { _ = removeIfExists(c.file) }
