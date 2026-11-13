package protocol

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/dunglas/httpsfv"
)

// RateLimitField is the field a send's answer carries its sender's budgets
// in (PROTOCOL.md §11.10).
const RateLimitField = "Dromas-RateLimit"

// DefaultWindow is a budget's window where its member gives no w: a day, in
// seconds (PROTOCOL.md §11.5).
const DefaultWindow = 86400

// maxSFInteger is the largest Integer RFC 9651 §3.3.1 lets a field carry.
const maxSFInteger = 999_999_999_999_999

// Budget is one member of Dromas-RateLimit: a budget of the sending
// mailbox's, as a send's answer reports it after the call (PROTOCOL.md
// §11.5). It is a token bucket that holds Cap and gets Refill back over each
// Window seconds, one every Interval.
type Budget struct {
	Left   int64 // the member's value: what the budget has left
	Cap    int64 // c: what the budget holds when full
	Refill int64 // q: what comes back in each window
	Window int64 // w: the window, in seconds; DefaultWindow where the member gives none
	Full   int64 // f: whole seconds until the budget is full, rounded up
}

// Interval is the time one unit takes to come back: Window divided by
// Refill.
func (b Budget) Interval() time.Duration {
	return time.Duration(b.Window) * time.Second / time.Duration(b.Refill)
}

// valid reports whether b is a budget §11.5 lets a member describe: every
// value in RFC 9651's range, Cap, Refill and Window at least 1, Left and
// Full at least 0, and Left at most Cap.
func (b Budget) valid() bool {
	in := func(v, low int64) bool { return v >= low && v <= maxSFInteger }
	return in(b.Cap, 1) && in(b.Refill, 1) && in(b.Window, 1) && in(b.Left, 0) && in(b.Full, 0) && b.Left <= b.Cap
}

// RateLimit is a Dromas-RateLimit field: the budgets it reports, by the
// quota each is (PROTOCOL.md §11.5).
type RateLimit map[Quota]Budget

// String writes r as the field's value, its members in the order of their
// keys, each budget's w left out where it is DefaultWindow. A member that is
// no valid budget, or whose key is no RFC 9651 key, is left out.
func (r RateLimit) String() string {
	d := httpsfv.NewDictionary()
	for _, q := range slices.Sorted(maps.Keys(r)) {
		b := r[q]
		if !b.valid() || !sfKey(string(q)) {
			continue
		}
		item := httpsfv.NewItem(b.Left)
		item.Params.Add("c", b.Cap)
		item.Params.Add("q", b.Refill)
		if b.Window != DefaultWindow {
			item.Params.Add("w", b.Window)
		}
		item.Params.Add("f", b.Full)
		d.Add(string(q), item)
	}
	v, err := httpsfv.Marshal(d)
	if err != nil {
		return ""
	}
	return v
}

// sfKey reports whether k is a key RFC 9651 §3.1.2 lets a dictionary have.
func sfKey(k string) bool {
	if k == "" || k[0] != '*' && (k[0] < 'a' || k[0] > 'z') {
		return false
	}
	return strings.Trim(k, "abcdefghijklmnopqrstuvwxyz0123456789_-.*") == ""
}

// ParseRateLimit reads a Dromas-RateLimit field, lines as an answer carries
// them (http.Header.Values), as PROTOCOL.md §11.5 has a client read it. A
// field that is no RFC 9651 Dictionary, or that is absent, gives nil: no
// reading, never "no limit". Otherwise it gives each member that describes a
// valid budget: an Integer whose parameters c, q and f are Integers, and w an
// Integer where it is given. A member of any other form is left out alone,
// and a key or a parameter it does not know is ignored. It never fails, so
// whatever a server sends costs its reader no more than no reading.
func ParseRateLimit(lines []string) RateLimit {
	if len(lines) == 0 {
		return nil
	}
	d, err := httpsfv.UnmarshalDictionary(lines)
	if err != nil {
		return nil
	}
	r := RateLimit{}
	for _, k := range d.Names() {
		m, _ := d.Get(k)
		item, ok := m.(httpsfv.Item)
		if !ok {
			continue
		}
		left, ok := item.Value.(int64)
		if !ok {
			continue
		}
		b := Budget{Left: left, Window: DefaultWindow}
		if !param(item.Params, "c", &b.Cap, true) || !param(item.Params, "q", &b.Refill, true) ||
			!param(item.Params, "w", &b.Window, false) || !param(item.Params, "f", &b.Full, true) || !b.valid() {
			continue
		}
		r[Quota(k)] = b
	}
	return r
}

// param reads the Integer parameter name of p into v, reporting false where
// it is of another type, or absent and needed.
func param(p *httpsfv.Params, name string, v *int64, needed bool) bool {
	x, ok := p.Get(name)
	if !ok {
		return !needed
	}
	n, ok := x.(int64)
	if ok {
		*v = n
	}
	return ok
}
