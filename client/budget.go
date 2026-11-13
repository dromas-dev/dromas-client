package client

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// budgetKey is where the mailbox's own records keep its estimate of its sends
// a day at its server (bucketMailbox).
const budgetKey = "sends-budget"

// budget is this mailbox's estimate of its sends a day, worked out from the
// Dromas-RateLimit its server's send answers carry (PROTOCOL.md §11.5): a
// token bucket that holds Cap, gets one back each Interval, and is full again
// at FullAt. A send that would find it empty waits in the outbox, with no
// call made. The estimate is advisory: the server's own count decides, and a
// refusal the estimate did not foresee waits as any refusal does.
type budget struct {
	Cap      int64         `json:"cap"`
	Interval time.Duration `json:"-"`
	// IntervalNS is Interval as it is kept: an integer, as every member the
	// state's reader takes is (protocol.Unmarshal).
	IntervalNS int64     `json:"interval_ns"`
	FullAt     time.Time `json:"full_at"`
}

// left is what the budget holds at now.
func (g *budget) left(now time.Time) int64 {
	d := g.FullAt.Sub(now)
	if d <= 0 {
		return g.Cap
	}
	taken := int64((d + g.Interval - 1) / g.Interval)
	return max(g.Cap-taken, 0)
}

// due is when k sends fit in the budget, at or before now where they fit
// already; ok is false where k is more than the budget ever holds.
func (g *budget) due(k int64, now time.Time) (at time.Time, ok bool) {
	if k > g.Cap {
		return time.Time{}, false
	}
	if g.left(now) >= k {
		return now, true
	}
	return g.FullAt.Add(-time.Duration(g.Cap-k) * g.Interval), true
}

// readBudget is the estimate the mailbox keeps, nil for none: a mailbox with
// no reading takes its budget as full.
func readBudget(s store) *budget {
	raw := s.get(bucketMailbox, budgetKey)
	if raw == nil {
		return nil
	}
	var g budget
	if protocol.Unmarshal(raw, &g) != nil || g.Cap < 1 || g.IntervalNS <= 0 {
		return nil
	}
	g.Interval = time.Duration(g.IntervalNS)
	return &g
}

// noteSendAnswer brings the estimate up to a send's answer at now: h are its
// header fields, and fresh is whether the server stored a new message (a 201)
// rather than answering a duplicate (a 200), which spends nothing.
//
// The answer's sends, where it carries a member a reader can use, is the
// reading: the estimate's full time becomes the later of the reading's and
// its own, projected through this post, so the lower estimate stands, since
// a server process whose view of the mailbox's sends lags reads high. An
// answer with no such member is no reading, never "no limit", and the
// estimate goes: the mailbox then takes its budget as full, and a refusal
// it did not foresee waits as any refusal does. That is the plainest
// recovery from a field absent or broken on the way, and costs at most the
// refusals of one burst.
func (b *Mailbox) noteSendAnswer(h http.Header, fresh bool, now time.Time) error {
	reading, ok := protocol.ParseRateLimit(h.Values(protocol.RateLimitField))[protocol.QuotaSends]
	return b.update(func(s store) error {
		if !ok {
			return s.del(bucketMailbox, budgetKey)
		}
		g := nextBudget(readBudget(s), reading, fresh, now)
		g.IntervalNS = int64(g.Interval)
		raw, err := json.Marshal(g)
		if err != nil {
			return err
		}
		return s.put(bucketMailbox, budgetKey, raw)
	})
}

// nextBudget is the estimate after a send's answer at now that reports
// reading, kept being the estimate before it, nil for none: the reading's
// full time, or kept's projected through this post where fresh, whichever is
// later. A kept estimate of another size or refill is not projected.
func nextBudget(kept *budget, reading protocol.Budget, fresh bool, now time.Time) budget {
	g := budget{Cap: reading.Cap, Interval: reading.Interval(), FullAt: now.Add(time.Duration(reading.Full) * time.Second)}
	if kept != nil && kept.Interval == g.Interval && kept.Cap == g.Cap {
		full := kept.FullAt
		if fresh {
			full = later(full, now).Add(g.Interval)
		}
		g.FullAt = later(g.FullAt, full)
	}
	return g
}

// later is the later of a and b.
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// budgetDue is when k sends fit in the budget this mailbox estimates: at
// once with no estimate, or where k is more than the budget ever holds, so
// the server decides as for any send.
func (b *Mailbox) budgetDue(k int64) (time.Time, error) {
	now := b.now()
	var g *budget
	if err := b.view(func(s store) error { g = readBudget(s); return nil }); err != nil || g == nil {
		return now, err
	}
	if at, ok := g.due(k, now); ok {
		return at, nil
	}
	return now, nil
}
