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
		for range 4 { // past the health check's time, more than once
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

// The health check is above a hold and its margin, so no live hold reaches it, and
// a hold that meets a connection its last one found dead ends before it can be
// taken for a drop of its own.
func TestTheHealthCheckIsAboveAHoldAndClosesWithinOne(t *testing.T) {
	after, wait := client.HealthCheck()
	bound := client.DefaultHold + 15*time.Second
	if after <= bound {
		t.Errorf("a ping is due after %v, and a live hold may read nothing for %v", after, bound)
	}
	if excess := after - bound + wait; excess >= client.MinHold/2 {
		t.Errorf("a hold on a dead connection lasts %v past the last's bound, and %v is taught", excess, client.MinHold/2)
	}
}

// One silent drop: its hold is cut at its bound, the health check closes the dead
// connection, and the next is a new one with keep-alive below the drop. It is
// taught once, so the wait stays and the mailbox is back within one hold's time
// and the check's, where a kernel might take 15 minutes.
func TestASilentDropOverHTTP2GetsANewConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, m := newHTTP2Mailbox(t)
		dead := m.last()
		start := time.Now()
		dropped := time.AfterFunc(2*time.Minute, func() { dead.forgotten.Store(true) })
		defer dropped.Stop()
		var settled *client.HoldResult
		for range 8 {
			h, err := m.b.Hold(t.Context(), client.DefaultHold)
			if err == nil && !h.Dropped {
				settled = h
				break
			}
		}
		took := time.Since(start)
		if settled == nil {
			t.Fatalf("never settled: %d connections in %v", m.dials(), took)
		}
		if m.dials() != 2 {
			t.Errorf("%d connections, want the dead one and one more", m.dials())
		}
		select {
		case <-dead.closed:
		default:
			t.Error("the dead connection is still open")
		}
		// Its hold's bound, the check's excess, a turn, and the next hold whole.
		if most := 5*time.Minute + 25*time.Second + time.Minute + client.DefaultHold; took > most {
			t.Errorf("back after %v, want at most %v", took, most)
		}
		if ka := client.KeepAliveOf(m.b); ka != 2*time.Minute+22*time.Second {
			t.Errorf("keep-alive %v: one drop teaches once", ka)
		}
		if m.lives[1] != 2*time.Minute+22*time.Second {
			t.Errorf("the new connection dialled with keep-alive %v", m.lives[1])
		}
		if settled.After != client.DefaultHold {
			t.Errorf("the wait is %v, which one drop does not shorten", settled.After)
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
