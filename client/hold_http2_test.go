package client_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// forgetful is a connection a network can forget: from then on nothing it sends
// arrives, and nothing is read from it, with no word, until it is closed.
type forgetful struct {
	net.Conn
	forgotten atomic.Bool
	closed    chan struct{}
	close     sync.Once
	written   atomic.Int64
}

func (c *forgetful) Read(p []byte) (int, error) {
	for {
		n, err := c.Conn.Read(p)
		if !c.forgotten.Load() {
			return n, err
		}
		if err != nil { // what the server sends is lost, and its end is too
			<-c.closed
			return 0, net.ErrClosed
		}
	}
}

func (c *forgetful) Write(p []byte) (int, error) {
	c.written.Add(int64(len(p)))
	if c.forgotten.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *forgetful) Close() error {
	c.close.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

// http2Mailbox is a mailbox of the default client over HTTP/2, and the connections
// it has dialled.
type http2Mailbox struct {
	b     *client.Mailbox
	mu    sync.Mutex
	conns []*forgetful
	lives []time.Duration // the keep-alive each was dialled with
}

func (m *http2Mailbox) last() *forgetful {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns[len(m.conns)-1]
}

func (m *http2Mailbox) dials() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}

func newHTTP2Mailbox(t *testing.T) (*env, *http2Mailbox) {
	t.Helper()
	e := &env{t: t, clock: &clock{t: time.Now().UTC().Truncate(time.Second)}}
	e.srv = fakeserver.NewInMemoryHTTP2(t, e.clock.Now)
	e.http = e.srv.Client()
	m := &http2Mailbox{b: e.mailbox()}
	// The server's transport makes each connection, as a *tls.Conn over its
	// in-memory network; a new one over the same bytes goes through forgetful.
	server := e.http.Transport.(*http.Transport)
	config := server.TLSClientConfig.Clone()
	config.NextProtos = []string{"h2"}
	client.UseDefaultClient(m.b, func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := server.DialTLSContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		f := &forgetful{Conn: c.(*tls.Conn).NetConn(), closed: make(chan struct{})}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.conns = append(m.conns, f)
		m.lives = append(m.lives, client.KeepAliveOf(m.b))
		return tls.Client(f, config), nil
	})
	receive(t, m.b)
	if m.dials() != 1 {
		t.Fatalf("%d connections for one receive", m.dials())
	}
	return e, m
}

// A live connection never pays for the health check: its holds end before it is
// due, and nothing is written from a hold's request to its answer.
func TestAHeldConnectionOverHTTP2SendsNothingWhileItIsLive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, m := newHTTP2Mailbox(t)
		for i := range 4 { // past the health check's time, more than once
			if i > 0 { // a gap, as long as a connection stays with nothing on it
				time.Sleep(85 * time.Second)
			}
			done := make(chan *client.HoldResult, 1)
			go func() {
				h, err := m.b.Hold(t.Context(), client.DefaultHold)
				if err != nil {
					t.Error(err)
				}
				done <- h
			}()
			time.Sleep(time.Minute + time.Second) // past its turn, whichever hold this is
			before := m.last().written.Load()
			h := <-done
			if h == nil || h.Dropped || h.After < client.DefaultHold-2*time.Minute {
				t.Fatalf("%+v", h)
			}
			if after := m.last().written.Load(); after != before {
				t.Errorf("%d bytes written while the server held the list", after-before)
			}
		}
		if m.dials() != 1 {
			t.Errorf("%d connections", m.dials())
		}
	})
}

// One silent drop: its hold is cut at its bound, and the next is on a new connection
// with keep-alive below the drop, at once, as over HTTP/1.1, whose cancelled
// request closes its connection. The wait stays.
func TestASilentDropOverHTTP2GetsANewConnectionAtItsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, m := newHTTP2Mailbox(t)
		dead := m.last()
		dead.forgotten.Store(true)
		start := time.Now()
		h, err := m.b.Hold(t.Context(), client.DefaultHold)
		if err != nil || !h.Dropped || time.Since(start) != client.DefaultHold+15*time.Second {
			t.Fatalf("%+v, %v, after %v", h, err, time.Since(start))
		}
		h, err = m.b.Hold(t.Context(), client.DefaultHold)
		if err != nil || h.Dropped || h.After != client.DefaultHold {
			t.Fatalf("the next hold: %+v, %v", h, err)
		}
		if m.dials() != 2 || m.lives[1] != 2*time.Minute+22*time.Second {
			t.Errorf("%d connections, the new one with keep-alive %v", m.dials(), m.lives[1])
		}
	})
}

// Holds shorter than the health check find a dead connection at each one's bound,
// not the check's.
func TestAShortHoldOverHTTP2FindsADeadConnectionAtItsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, m := newHTTP2Mailbox(t)
		m.last().forgotten.Store(true)
		h, err := m.b.Hold(t.Context(), time.Minute)
		if err != nil || !h.Dropped {
			t.Fatalf("%+v, %v", h, err)
		}
		if h, err = m.b.Hold(t.Context(), time.Minute); err != nil || h.Dropped || m.dials() != 2 {
			t.Fatalf("the next hold: %+v, %v, %d connections", h, err, m.dials())
		}
	})
}

// A call that is no hold, on a connection dead with no hold to find it: it fails at
// its own bound, and the next is on a new connection and goes through.
func TestACallOverHTTP2AfterADropGetsANewConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, m := newHTTP2Mailbox(t)
		a := e.mailbox()
		m.last().forgotten.Store(true)
		if res, err := m.b.Receive(t.Context()); err == nil {
			t.Fatalf("a receive on a dead connection: %+v", res)
		}
		if _, err := m.b.Send(t.Context(), a.Address(), "after the drop"); err != nil {
			t.Errorf("the next call: %v", err)
		}
		if m.dials() != 2 {
			t.Errorf("%d connections", m.dials())
		}
	})
}

// A live connection a proxy stops streams on, keeping the connection and answering
// PINGs, teaches at each hold: a proxy's timeout is what shortens the wait (hold.go),
// and it settles as over HTTP/1.1.
func TestAProxysTimeoutOverHTTP2SettlesAsItDoesOverHTTP1(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, m := newHTTP2Mailbox(t)
		path{2 * time.Minute, true, false}.on(e, m.b)
		holds, h, _ := settle(t, m.b)
		if holds != 7 || h.After != time.Minute+11*time.Second || client.KeepAliveOf(m.b) != client.MinHold/2 {
			t.Errorf("settled after %d holds, holding %v with keep-alive %v", holds, h.After, client.KeepAliveOf(m.b))
		}
	})
}

// Whatever a call is waiting on, a connection that goes dead is closed by the health
// check, in its time and not the kernel's: no hold's bound or cancel comes into it.
func TestADeadConnectionOverHTTP2IsClosedByItsHealthCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, m := newHTTP2Mailbox(t)
		e.srv.SetMiddle(func(_ http.ResponseWriter, r *http.Request, _ http.Handler) { <-r.Context().Done() })
		dead := m.last()
		ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.srv.URL+"/v1/", nil)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		done := make(chan error, 1)
		go func() {
			resp, err := client.HTTPOf(m.b).Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
			done <- err
		}()
		synctest.Wait()
		dead.forgotten.Store(true)
		err = <-done
		after, wait := client.HealthCheck()
		if took := time.Since(start); err == nil || ctx.Err() != nil || took > after+wait {
			t.Errorf("after %v: %v (the context: %v), want it closed by %v", took, err, ctx.Err(), after+wait)
		}
		select {
		case <-dead.closed:
		default:
			t.Error("the dead connection is still open")
		}
	})
}

// A list's answer that stalls in its body is cut at the bound as one with no head
// is, and taught as silent: the mailbox did not see it as it came.
func TestAnAnswerThatStallsInItsBodyOverHTTP2IsSilent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, m := newHTTP2Mailbox(t)
		e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if !r.URL.Query().Has("wait") {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"count":`))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		})
		h, err := m.b.Hold(t.Context(), client.DefaultHold)
		if err != nil || !h.Dropped {
			t.Fatalf("%+v, %v", h, err)
		}
		// Silent, keep-alive halves from the wait; seen as it came, it is just
		// below how long the hold lived.
		if h.KeepAlive != 2*time.Minute+22*time.Second {
			t.Errorf("keep-alive %v", h.KeepAlive)
		}
	})
}
