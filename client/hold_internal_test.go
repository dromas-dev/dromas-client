package client

import (
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestTheDefaultClientResumesTLSOnANewConnection: a connection the default
// client makes after another to the same server resumes the TLS session,
// rather than making a full handshake.
func TestTheDefaultClientResumesTLSOnANewConnection(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	tr := defaultTransport(&holding{})
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr.TLSClientConfig.RootCAs = pool
	c := &http.Client{Transport: tr}
	var resumed []bool
	for range 2 {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		resumed = append(resumed, resp.TLS.DidResume)
		c.CloseIdleConnections() // the next request makes a new connection
	}
	if resumed[0] || !resumed[1] {
		t.Fatalf("resumed %v, want a full handshake then a resumed one", resumed)
	}
}

// TestNoKeepAliveUntilAConnectionDrops: the default client's dialer sends
// no keep-alive, where net's default sends it, until a connection drops
// part way through a hold; the next then keeps alive below the drop.
func TestNoKeepAliveUntilAConnectionDrops(t *testing.T) {
	var h holding
	// net.Dialer: keep-alive is off only with KeepAliveConfig.Enable false
	// and KeepAlive negative.
	if d := h.dialer(); d.KeepAliveConfig.Enable || d.KeepAlive >= 0 {
		t.Fatalf("a mailbox that has seen no drop dials with keep-alive: %+v", d)
	}
	h.dropped(3*time.Minute, false)
	d := h.dialer()
	if ka := d.KeepAliveConfig; !ka.Enable || ka.Idle != 3*time.Minute-holdMargin || ka.Interval != ka.Idle {
		t.Fatalf("after a drop at 3 minutes the dialer keeps alive with %+v", ka)
	}
}

// TestASilentDropHalvesTheKeepAlive: a drop seen only at the hold's
// deadline came at some time within the wait, so keep-alive halves at each,
// from the wait, to its floor; only then does the wait halve, to MinHold.
func TestASilentDropHalvesTheKeepAlive(t *testing.T) {
	var h holding
	held := DefaultHold + holdMargin
	type step struct{ wait, keepAlive time.Duration }
	var got []step
	for range 7 {
		w, ka := h.dropped(held, true)
		got = append(got, step{w, ka})
	}
	want := []step{
		{DefaultHold, 2*time.Minute + 22*time.Second},
		{DefaultHold, time.Minute + 11*time.Second},
		{DefaultHold, 35 * time.Second},
		{DefaultHold, minKeepAlive},
		{2*time.Minute + 22*time.Second, minKeepAlive},
		{time.Minute + 11*time.Second, minKeepAlive},
		{MinHold, minKeepAlive},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("silent drops came to %v, want %v", got, want)
		}
	}
}

// Holds can meet one dead connection several times before its health check closes
// it, and one drop is taught once; another connection's teaches again.
func TestADeadConnectionTeachesOnce(t *testing.T) {
	var h holding
	one, two := &net.TCPConn{}, &net.TCPConn{}
	_, first := h.droppedOn(one, 5*time.Minute, true)
	if _, ka := h.droppedOn(one, 5*time.Minute, true); ka != first {
		t.Errorf("keep-alive %v, then %v from one connection", first, ka)
	}
	if _, ka := h.droppedOn(one, time.Minute, false); ka != first {
		t.Errorf("keep-alive %v, then %v when it closed", first, ka)
	}
	if _, ka := h.droppedOn(two, 5*time.Minute, true); ka >= first {
		t.Errorf("keep-alive %v, then %v from another connection", first, ka)
	}
	// A hold that never got a connection names none to have taught.
	var none holding
	_, a := none.droppedOn(nil, 5*time.Minute, true)
	if _, b := none.droppedOn(nil, 5*time.Minute, true); b >= a {
		t.Errorf("keep-alive %v, then %v", a, b)
	}
}

type closeCounter struct {
	http.RoundTripper
	closed int
}

func (c *closeCounter) CloseIdleConnections() { c.closed++ }

// A mailbox's client closes its idle connections through the transport every
// mailbox's calls go through: http.Client reaches only one that has the method.
func TestClosingIdleConnectionsReachesTheTransport(t *testing.T) {
	next := &closeCounter{}
	c := &http.Client{Transport: transport{next}}
	c.CloseIdleConnections()
	if next.closed != 1 {
		t.Errorf("reached it %d times", next.closed)
	}
	(&http.Client{Transport: transport{roundTripperOnly{}}}).CloseIdleConnections() // has no method: nothing to reach
}

type roundTripperOnly struct{ http.RoundTripper }
