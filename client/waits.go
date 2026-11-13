package client

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// bucketWaits keeps, for each outbox entry that a refusal or the budget made
// wait, when it may be posted again, by its name in outbox/ less ".P"
// (<address>.<msg_id>), in RFC 3339 to the nanosecond, so a wait of a
// Retry-After in whole seconds is due exactly then. An entry it names
// no time for is due at once. The time goes when the entry is posted or moved
// to outbox/failed/.
const bucketWaits = "waits"

// Queued is an outbox entry that waits: the mailbox it goes to, the msg_id
// its envelope carries, and when it is posted, at the next flush that runs
// then or after.
type Queued struct {
	To    protocol.Address
	MsgID string
	At    time.Time
}

// Flushed is what a flush did: each message the server accepted, and each
// entry that waits.
type Flushed struct {
	Sent   []*Sent
	Queued []Queued
}

// waitFor keeps that the outbox entry stem waits until at.
func (b *Mailbox) waitFor(stem string, at time.Time) error {
	return b.update(func(s store) error {
		return s.put(bucketWaits, filepath.Base(stem), []byte(at.UTC().Format(time.RFC3339Nano)))
	})
}

// waitOf is when the outbox entry stem may be posted, zero for at once.
func waitOf(s store, stem string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, string(s.get(bucketWaits, filepath.Base(stem))))
	if err != nil {
		return time.Time{}
	}
	return t
}

// waitAfter keeps the wait a post refused err leaves the outbox entry stem:
// the server's Retry-After, where it gave one. With none the entry is due at
// the next flush, and any wait kept for it goes.
func (b *Mailbox) waitAfter(stem string, err error) error {
	if d := waitAfter(err); d > 0 {
		return b.waitFor(stem, b.now().Add(d))
	}
	return b.unwait(stem)
}

// unwait forgets the wait of the outbox entry stem: it was posted, or moved.
func (b *Mailbox) unwait(stem string) error {
	return b.update(func(s store) error { return s.del(bucketWaits, filepath.Base(stem)) })
}

// waitAfter is how long a post refused err leaves its entry waiting: the
// server's Retry-After where it gave one, and none otherwise, so the entry
// is due at the next flush.
func waitAfter(err error) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.RetryAfter
	}
	return 0
}

// aboutTheSender reports whether a post's failure err is about the sender or
// the path to the server rather than about its recipient (PROTOCOL.md §12.2):
// a 429 rate_limited, counted against the sending mailbox or its IP address,
// a 503 or any other 5xx, or no answer. The posts after it would meet the
// same, so a flush posts nothing more. A recipient's own mailbox_full and
// mailbox_rate_limited, and every refusal of the client's own, are about
// that recipient alone.
func aboutTheSender(err error) bool {
	var te *transportError
	if errors.As(err, &te) {
		return true
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status >= 500 || ae.Problem.Code == protocol.CodeRateLimited
}
