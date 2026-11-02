package client

import (
	"crypto/x509"
	"io"
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

type closeCounter struct {
	http.RoundTripper
	closed int
}

func (c *closeCounter) CloseIdleConnections() { c.closed++ }

// A mailbox's client closes its idle connections through the transport every
// mailbox's calls go through: http.Client reaches only one that has the method.
func TestClosingIdleConnectionsReachesTheTransport(t *testing.T) {
	next := &closeCounter{}
	c := &http.Client{Transport: newRoundTripper(next)}
	c.CloseIdleConnections()
	if next.closed != 1 {
		t.Errorf("reached it %d times", next.closed)
	}
	(&http.Client{Transport: newRoundTripper(roundTripperOnly{})}).CloseIdleConnections() // has no method: nothing to reach
}

type roundTripperOnly struct{ http.RoundTripper }

// The next call after renew has a new connection and resumes TLS: its clone keeps
// the session cache, and the settings, of the transport it was cloned from.
func TestARenewedTransportResumesAndKeepsItsHealthCheck(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	tr := defaultTransport(&holding{})
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	tr.TLSClientConfig.RootCAs = pool
	wrapped := newRoundTripper(tr)
	c := &http.Client{Transport: wrapped}
	var resumed []bool
	for range 2 {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.ProtoMajor != 2 {
			t.Fatalf("HTTP/%d", resp.ProtoMajor)
		}
		resumed = append(resumed, resp.TLS.DidResume)
		wrapped.renew(wrapped.current())
	}
	if resumed[0] || !resumed[1] {
		t.Errorf("resumed %v, want a full handshake then a resumed one", resumed)
	}
	now := (*wrapped.next.Load()).(*http.Transport)
	if now == tr || now.HTTP2 == nil || now.HTTP2.SendPingTimeout != healthCheckAfter {
		t.Errorf("after renew: %+v", now.HTTP2)
	}
	wrapped.CloseIdleConnections()
}

// No live connection is pinged: one idle closes at connectionIdle, and the hold that
// might follow that ends by its wait and margin.
func TestTheHealthCheckIsAboveAnythingALiveConnectionDoes(t *testing.T) {
	tr := defaultTransport(&holding{})
	if most := tr.IdleConnTimeout + DefaultHold + holdMargin; tr.HTTP2.SendPingTimeout < most {
		t.Errorf("a ping is due after %v, and a live connection may read nothing for %v", tr.HTTP2.SendPingTimeout, most)
	}
	if connectionIdle != http.DefaultTransport.(*http.Transport).IdleConnTimeout {
		t.Logf("connectionIdle %v differs from net/http's default", connectionIdle)
	}
}

// A renew by a call on what has since been renewed leaves the new connection be.
func TestARenewAfterAnotherLeavesItsTransport(t *testing.T) {
	tr := newRoundTripper(defaultTransport(&holding{}))
	was := tr.current()
	tr.renew(was)
	now := tr.current()
	if now == was {
		t.Fatal("no new transport")
	}
	tr.renew(was)
	if tr.current() != now {
		t.Error("a second renew by the old one replaced the new one")
	}
}
