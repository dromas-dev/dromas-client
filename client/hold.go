package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
	"golang.org/x/time/rate"
)

// A mailbox that waits for messages holds its list (PROTOCOL.md §11.11), one held
// request after another, down one TLS connection that it keeps. Two layers
// fail and recover apart:
//
//   - The connection. The default client keeps one, resumes TLS when it
//     makes another (a session cache, which a new process reads back:
//     ticketCache), and sends no keep-alive on it:
//     every probe across many held connections is traffic, and the answer
//     that ends each hold is traffic enough for most networks. A connection
//     that drops before its hold ends is a network that forgets a silent
//     connection sooner; the next is made with TCP keep-alive below the
//     drop, so the connection lives and the wait stays long. A drop the
//     mailbox sees as it comes (a reset) names when the network forgot the
//     connection. One it sees only at its own deadline (a network that
//     drops silently) names only a bound above that, so keep-alive halves
//     at each such drop: a search for a bound no answer names, as path MTU
//     discovery's binary search halves its range at each failed probe
//     (RFC 4821 §7.3).
//   - An HTTP/2 connection. A cancelled stream leaves its connection in the
//     pool, dead or not, where HTTP/1.1 closes it, and net/http can close it
//     only once it is idle, which a stream cut this moment is not yet. So a
//     call cut by its bound moves the mailbox to a clone of its transport
//     (transport.renew), and the next call makes a new connection, as over
//     HTTP/1.1; streams still on the old one go on, and it closes when they
//     are done. What is left open past that is HTTP/2's own health check's,
//     as Kubernetes' client closes a broken connection the kernel would hold
//     for 13 to 30 minutes (apimachinery's pkg/util/net/http.go): a PING
//     after healthCheckAfter with no frame read, closed if healthCheckWait
//     passes unanswered. Theirs pings at 30 s; here that is traffic across
//     many held connections for what a hold's bound finds anyway, so it is
//     set above anything a live connection does.
//   - The request. Each asks the server to hold it for the mailbox's wait,
//     DefaultHold at first, and ends at that wait and a margin from the
//     connection it got last, whether answered or not: a dead path is found
//     there, never by a probe on a live one. Only a connection that drops with keep-alive
//     at its floor shortens the wait, never below MinHold: a path whose
//     probes keep nothing alive (an HTTP proxy's timeout, which TCP's
//     keep-alive does not reach). Holds start at most about once a minute
//     (holdEvery), so a connection that fails at once is a minute's poll,
//     within the poll budget (PROTOCOL.md §11.5).

// DefaultHold is how long a mailbox asks the server to hold its list at first:
// under the 5 minutes at which common networks drop a silent connection.
const DefaultHold = 4*time.Minute + 45*time.Second

// MinHold is the shortest wait a mailbox asks for, however its connections
// drop: the poll budget covers a list a minute.
const MinHold = time.Minute

// holdMargin is how long after its wait a held request may go unanswered
// before the mailbox takes its connection for dead: the server answers at the
// wait at the latest.
const holdMargin = 15 * time.Second

// healthCheckAfter is how long an HTTP/2 connection goes with no frame read
// before it is sent a PING, and healthCheckWait how long that PING may go
// unanswered before the connection is closed (http.HTTP2Config). The first is
// the longest a live connection reads nothing: an idle one closes at
// connectionIdle, and a hold follows that for its wait and margin, so no live
// connection is pinged. It is above gRPC's 5 minutes, the least a server of
// theirs takes pings at by default (gRFC A8). The second is Kubernetes'.
const (
	healthCheckAfter = connectionIdle + DefaultHold + holdMargin
	healthCheckWait  = 15 * time.Second
)

// connectionIdle is how long the default client keeps a connection nothing is on,
// net/http's default transport's IdleConnTimeout.
const connectionIdle = 90 * time.Second

// holdEvery and holdBurst pace the holds a mailbox starts: one a minute on
// average, two at once.
const (
	holdEvery = time.Minute
	holdBurst = 2
)

// dialTimeout bounds a connection's making, and the default transport's
// TLS handshake has its own (net/http's TLSHandshakeTimeout).
const dialTimeout = 10 * time.Second

// holding is what a mailbox has learned of its network while it holds its
// list: the wait it asks for, and the keep-alive its connections need.
type holding struct {
	mu        sync.Mutex
	wait      time.Duration // the wait it asks for; DefaultHold when zero
	keepAlive time.Duration // the keep-alive a new connection gets; none when zero
	starts    *rate.Limiter
	http2     atomic.Bool // the connection in use speaks HTTP/2
}

func (h *holding) current() (wait, keepAlive time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wait == 0 {
		h.wait = DefaultHold
	}
	return h.wait, h.keepAlive
}

// minKeepAlive is the shortest keep-alive a mailbox's connections get, however
// they drop.
const minKeepAlive = MinHold / 2

// dropped records a connection that dropped after held. One seen as it
// dropped gives the next connection keep-alive below held; one seen only at
// the hold's own deadline (silent) dropped at some time within the wait, so
// keep-alive halves, from the wait at first. Once keep-alive is at its
// floor, a drop shortens the wait instead: below held, or for a silent one
// to half of it. It returns what the mailbox asks for and sets from now on.
func (h *holding) dropped(held time.Duration, silent bool) (wait, keepAlive time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wait == 0 {
		h.wait = DefaultHold
	}
	below := held - holdMargin
	if silent {
		below = h.keepAlive
		if below == 0 {
			below = min(h.wait, held)
		}
		below = (below / 2).Truncate(time.Second)
	}
	below = max(below, minKeepAlive)
	switch {
	case h.keepAlive == 0 || h.keepAlive > below:
		h.keepAlive = below
	case silent:
		h.wait = max((h.wait / 2).Truncate(time.Second), MinHold)
	default:
		h.wait = max(min(h.wait, held)-holdMargin, MinHold)
	}
	return h.wait, h.keepAlive
}

func (h *holding) limiter() *rate.Limiter {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.starts == nil {
		h.starts = rate.NewLimiter(rate.Every(holdEvery), holdBurst)
	}
	return h.starts
}

// keepAliveOf is the keep-alive a connection of the default client gets:
// none, or what the mailbox's drops taught it (holding.dropped).
func (h *holding) keepAliveOf() net.KeepAliveConfig {
	_, ka := h.current()
	if ka == 0 {
		return net.KeepAliveConfig{Enable: false}
	}
	return net.KeepAliveConfig{Enable: true, Idle: ka, Interval: ka, Count: 3}
}

// dialer is what makes a connection of the default client: keep-alive as
// keepAliveOf says, and otherwise none, where net's default sends it.
func (h *holding) dialer() *net.Dialer {
	d := &net.Dialer{Timeout: dialTimeout, KeepAliveConfig: h.keepAliveOf()}
	if !d.KeepAliveConfig.Enable {
		d.KeepAlive = -1
	}
	return d
}

// defaultTransport is the default client's: net/http's own, a TLS session
// cache so that a new connection resumes (RFC 8446 §2.2; no early data; a
// mailbox with a directory wraps it in a ticketCache), HTTP/2's health check,
// and a dialer that sends keep-alive only as h says.
func defaultTransport(h *holding) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{ClientSessionCache: tls.NewLRUClientSessionCache(0)}
	t.IdleConnTimeout = connectionIdle
	t.HTTP2 = &http.HTTP2Config{SendPingTimeout: healthCheckAfter, PingTimeout: healthCheckWait}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return h.dialer().DialContext(ctx, network, addr)
	}
	return t
}

// HoldResult is what one held list came to.
type HoldResult struct {
	// Changed reports that the mailbox's list changed, or that the mailbox must
	// receive to find out (its pass refused, or no mailbox at this address): a
	// Receive follows.
	Changed bool
	// Dropped reports that a connection of the hold dropped: before the
	// hold's answer, seen as it dropped or only at the hold's deadline, or,
	// over HTTP/1.1, on a reused connection that net/http sent the request
	// again past, which the hold's answer then follows. Wait and KeepAlive are
	// then what the mailbox's default client asks for from now on: a given
	// client's dialer is its own, so nothing applies KeepAlive there.
	Dropped bool
	// After is how long the request was held on the connection it got
	// last.
	After           time.Duration
	Wait, KeepAlive time.Duration
}

// heldFor is the context key of the bound on a held list's call, in place
// of requestTimeout (exchange, heldBound).
type heldFor struct{}

// errHeldOut is a held list's call cut at its bound, unanswered.
var errHeldOut = fmt.Errorf("no answer within the wait: %w", context.DeadlineExceeded)

// heldBound is ctx, cut with errHeldOut once d passes from the connection
// its request got last: net/http may send a GET again on another connection,
// and that request's bound is its own.
func heldBound(ctx context.Context, d time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	t := time.AfterFunc(d, func() { cancel(errHeldOut) })
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { t.Reset(d) }})
	return ctx, func() {
		t.Stop()
		cancel(nil)
	}
}

// timedOut reports whether err is a call cut by a bound on its time, with
// no answer: a drop the mailbox did not see as it came.
func timedOut(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout()
}

// Transient reports whether err is a call that failed and that making it
// again may get past: no answer, a server error, a rate limit or a request
// timeout. So is a lock another command held for as long as a command waits for
// it, a renewal held back until renewRetry after one that failed so, a drain
// refused for what a renewal mends whose renewal failed so ([Mailbox.Receive]),
// and a refusal that this machine puts right without its owner ([NotReady]).
// A refusal no wait mends is not: [Fatal] names those.
func Transient(err error) bool {
	var ae *APIError
	var re *RevokedError
	var rh *renewalHeld
	var ro *renewalOwed
	switch {
	case err == nil, errors.As(err, &re):
		return false
	case errors.Is(err, errLocked), errors.As(err, &ro), NotReady(err):
		return true
	case errors.As(err, &rh):
		return rh.transient
	case errors.As(err, &ae):
		return ae.Status >= 500 || ae.Status == http.StatusTooManyRequests || ae.Status == http.StatusRequestTimeout
	}
	var te *transportError
	return errors.As(err, &te)
}

// Fatal reports whether err is a refusal no wait mends, which only the mailbox's
// owner or its server's operator can put right: a revocation, a mailbox in
// another format, a server's refusal that [Transient] does not take, such as no
// mailbox at this address that a registration again does not mend, or this
// client's own refusal. A supervisor that
// starts the process again gains nothing. An error that is neither Fatal nor
// Transient, such as a fault of the disk, ends a run and may pass.
func Fatal(err error) bool {
	var re *RevokedError
	var fe *FormatError
	var ae *APIError
	var rf *RefusedError
	switch {
	case err == nil, Transient(err):
		return false
	case errors.As(err, &re), errors.As(err, &fe), errors.As(err, &ae), errors.As(err, &rf):
		return true
	}
	return false
}

// turn waits for the mailbox's turn to start a hold, one a minute on average,
// for up to wait: false is a turn later than that, whose wait it has waited
// out instead.
func (h *holding) turn(ctx context.Context, wait time.Duration) (bool, error) {
	r := h.limiter().Reserve()
	d := r.Delay()
	if d >= wait {
		r.Cancel()
		d = max(wait, 0)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		r.Cancel()
		return false, ctx.Err()
	case <-t.C:
	}
	return d < wait, nil
}

// Hold holds the mailbox's list for up to wait, or the mailbox's own wait if that
// is shorter (DefaultHold, or what its drops taught it): the server answers
// once the mailbox's first page changes, or at the wait with nothing new. It
// first waits its turn (one a minute on average), and where the turn comes
// past the wait, it waits the wait out and holds nothing. It then holds the
// mailbox's lock only to read its tag and its pass, never through the hold, so
// the mailbox's other commands go on meanwhile. The held request reads nothing
// of the mailbox's state: an answer about a revocation is proved under the lock
// once it comes (provedLocked).
//
// A mailbox with no tag kept, one whose last list found messages, whose pass the
// server refused, that the server holds no mailbox for, or whose certificate in
// use its server shows revoked (renewalMends), is Changed at once, for a
// Receive to settle: the Receive signs another pass, or renews the prekey,
// which registers the mailbox again or makes a new certificate (mendable). An error is a
// call that failed and that holding again may get past ([Transient]), or a
// refusal no wait mends ([Fatal]).
func (b *Mailbox) Hold(ctx context.Context, wait time.Duration) (*HoldResult, error) {
	own, _ := b.holding.current()
	wait = min(wait, own)
	if ok, err := b.holding.turn(ctx, wait); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return &HoldResult{}, nil
	}
	type kept struct{ pass, tag, path string }
	k, err := locked(ctx, b, func() (kept, error) {
		if err := b.keysMatch(); err != nil {
			return kept{}, err
		}
		hdr, err := b.ifNoneMatch()
		if err != nil || hdr == nil {
			return kept{}, err
		}
		pass, err := b.pass()
		return kept{pass, hdr.Get("If-None-Match"), b.mailboxPath("/messages")}, err
	})
	if err != nil {
		return nil, err
	}
	if k.tag == "" {
		return &HoldResult{Changed: true}, nil
	}
	hdr := http.Header{"If-None-Match": {k.tag}, "Authorization": {k.pass}}
	path := k.path + "?wait=" + strconv.FormatInt(int64(wait/time.Second), 10)
	// How long the request was held is timed from the connection it got
	// last, and a drop is the connection's. Over HTTP/1.1 net/http sends a GET
	// again on a new connection when a reused one drops before its answer, so a
	// connection asked for after one was got is asked for past a drop, seen as it
	// came; it is learned then, before the next is dialled. Over HTTP/2 a lost
	// connection is not sent again past: one asked for then follows a server's
	// GOAWAY or REFUSED_STREAM, which says nothing of the network.
	h := &HoldResult{}
	var mu sync.Mutex
	var got bool
	start := time.Now()
	trace := &httptrace.ClientTrace{
		TLSHandshakeDone: func(s tls.ConnectionState, err error) {
			if err == nil {
				b.holding.http2.Store(s.NegotiatedProtocol == "h2")
			}
		},
		GetConn: func(string) {
			mu.Lock()
			defer mu.Unlock()
			if lived := time.Since(start); got && lived >= MinHold/2 && !b.holding.http2.Load() {
				h.Dropped = true
				h.Wait, h.KeepAlive = b.holding.dropped(lived, false)
			}
		},
		GotConn: func(httptrace.GotConnInfo) {
			mu.Lock()
			defer mu.Unlock()
			got, start = true, time.Now()
		},
	}
	held := httptrace.WithClientTrace(context.WithValue(ctx, heldFor{}, wait+holdMargin), trace)
	var list protocol.MessageList
	resp, err := b.exchange(held, http.MethodGet, path, nil, hdr, &list)
	mu.Lock()
	after := time.Since(start)
	h.After = after
	mu.Unlock()
	err = b.provedLocked(ctx, http.MethodGet, path, err)
	var te *transportError
	switch {
	case err == nil && resp.StatusCode == http.StatusNotModified && sameTag(resp.Header.Get("ETag"), k.tag):
	case err == nil, passRefused(code(err)), renewalMends(err):
		h.Changed = true
	case ctx.Err() == nil && errors.As(err, &te) && after >= MinHold/2:
		// The connection dropped part way through a hold, or its answer
		// never came: a network that forgets a silent connection.
		h.Dropped = true
		h.Wait, h.KeepAlive = b.holding.dropped(after, timedOut(err))
	case errors.As(err, &te):
		// No answer at all.
		return nil, err
	default:
		return nil, err
	}
	return h, nil
}

// ReceiveEvery is the longest Await holds before it returns for a Receive,
// which renews the prekey when it is due.
const ReceiveEvery = time.Hour

// Await holds the mailbox's list, one held request after another (Hold), until
// it changes, until passes or ReceiveEvery has, and returns for a Receive;
// a zero until is no deadline, as for a mailbox that waits for messages for as long
// as it runs. A call that holding again may get past (no answer, a server
// error, a rate limit) is held again, after its Retry-After where it gives
// one, and a refusal this machine puts right ([NotReady]) after ReceiveEvery or
// at until; a refusal no wait mends ([Fatal]) ends it, as its error. h, when not
// nil, is handed an [EventRetry] for each held request that failed and an
// [EventConnectionDropped] for each connection that dropped, and an error from
// h ends Await as that error. Await returns by until, its waits for a turn to
// hold included (Hold). [Mailbox.Follow] is the whole cycle, of which Await is
// the part that waits.
//
// With under a second left Await waits it out, so the Receive after is the
// caller's last, never one of a burst of polls until the deadline passes.
func (b *Mailbox) Await(ctx context.Context, until time.Time, h Handler) error {
	if h == nil {
		h = noHandler
	}
	if every := time.Now().Add(ReceiveEvery); until.IsZero() || every.Before(until) {
		until = every
	}
	for time.Until(until) >= time.Second {
		r, err := b.Hold(ctx, time.Until(until))
		var ae *APIError
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case Fatal(err):
			return err
		case err != nil:
			if herr := h(ctx, Event{Kind: EventRetry, Err: err}); herr != nil {
				return herr
			}
			var pause time.Duration
			switch {
			case NotReady(err):
				pause = ReceiveEvery
			case errors.As(err, &ae):
				pause = ae.RetryAfter
			}
			if pause > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(min(pause, time.Until(until))):
				}
			}
			continue
		}
		if r.Dropped {
			if herr := h(ctx, Event{Kind: EventConnectionDropped, Hold: r}); herr != nil {
				return herr
			}
		}
		if r.Changed {
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(until)):
	}
	return nil
}
