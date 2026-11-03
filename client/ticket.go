package client

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"net/url"
	"os"
	"slices"
	"strings"
)

// ticketFile is the file in the rotating keys' directory that keeps a TLS
// session ticket (client/doc.go).
const ticketFile = "tls-ticket"

// extraBase and extraTicket name what ticketCache adds to a session's
// tls.SessionState.Extra, the place crypto/tls gives a session cache for what
// it stores beside a session: the server's URL, and the ticket itself.
const (
	extraBase   = "dromas-base "
	extraTicket = "dromas-ticket "
)

// ticketCache is the default client's TLS session cache. The embedded cache
// holds what this process learned, of every server name. The file holds the
// last TLS 1.3 ticket of the mailbox's server, so a new process resumes TLS on
// its first connection (RFC 8446 §2.2). A ticket resumes a TLS session and
// authenticates nothing, so every call that needs a pass still carries one
// (PROTOCOL.md §8). A session read from the file offers no early data (decode).
//
// The ticket's secret is a key, and the state file holds no key, because a key
// deleted inside a database stays in its free pages (client/store.go). crypto/tls
// calls Put as a connection reads, during a Hold that holds no lock, and during a
// command that holds the state file. So Put writes the ticket's own file, through
// writeKey, with no lock and no transaction. Two processes that write at once
// leave one whole ticket, and either ticket resumes.
//
// Only a TLS 1.3 ticket goes in the file. A TLS 1.2 session's secret is its master
// secret, which decrypts that session's recorded traffic. A TLS 1.3 ticket's
// secret does not, and crypto/tls resumes TLS 1.3 with a new key agreement
// (psk_dhe_ke).
//
// A ticket is only an optimisation: a read or a write that fails costs a full
// handshake, fails no call, and is said nowhere.
type ticketCache struct {
	tls.ClientSessionCache
	file string // where the ticket is kept
	base string // the server URL the mailbox calls
	name string // the key crypto/tls gives that server's sessions
}

// newTicketCache is a ticketCache that keeps sessions of base, a URL
// checkServerURL took, in file. crypto/tls keys a session by tls.Config.ServerName,
// which net/http sets to the URL's host without its port. So a proxy that speaks
// TLS has another key, and goes no further than memory. Two ports of one host
// share a key, so the file names the whole URL too.
func newTicketCache(memory tls.ClientSessionCache, file, base string) *ticketCache {
	c := &ticketCache{ClientSessionCache: memory, file: file, base: base}
	if u, err := url.Parse(base); err == nil {
		c.name = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	return c
}

// Get is the session this process holds for key, or the ticket in the file where
// key is the server's and the process holds none.
func (c *ticketCache) Get(key string) (*tls.ClientSessionState, bool) {
	if cs, ok := c.ClientSessionCache.Get(key); ok || key != c.name {
		return cs, ok
	}
	cs := c.read()
	return cs, cs != nil
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

// encode is what the file holds of cs, or nil where the file does not take cs: no
// session, or one of a TLS version other than 1.3, which SessionState.Bytes writes
// first (the SessionState encoding, crypto/tls/ticket.go).
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
	raw, err := kept.Bytes()
	if err != nil || len(raw) < 2 || binary.BigEndian.Uint16(raw) != tls.VersionTLS13 {
		return nil
	}
	return raw
}

// read is the session in the file. A file that others can reach, cannot be read,
// or holds no ticket of this server URL gives none, and is removed, so a changed
// URL drops the ticket.
func (c *ticketCache) read() *tls.ClientSessionState {
	if err := checkKeyFile(c.file); err != nil {
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
func (c *ticketCache) decode(raw []byte) *tls.ClientSessionState {
	state, err := tls.ParseSessionState(raw)
	if err != nil {
		return nil
	}
	var base, ticket []byte
	for _, e := range state.Extra {
		if v, ok := bytes.CutPrefix(e, []byte(extraBase)); ok {
			base = v
		}
		if v, ok := bytes.CutPrefix(e, []byte(extraTicket)); ok {
			ticket = v
		}
	}
	if string(base) != c.base || len(ticket) == 0 {
		return nil
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
