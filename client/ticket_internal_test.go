package client

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/cryptobyte"
)

// ticketServer is a TLS server with fixed ticket keys, so two of them with the
// same keys resume each other's sessions, as one server on two ports does.
func ticketServer(t *testing.T, maxVersion uint16) *httptest.Server {
	t.Helper()
	return ticketServerOn(t, "", maxVersion)
}

// ticketServerOn is ticketServer listening on addr, or on httptest's own
// address where addr is empty.
func ticketServerOn(t *testing.T, addr string, maxVersion uint16) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if addr != "" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = srv.Listener.Close()
		srv.Listener = l
	}
	srv.TLS = &tls.Config{MaxVersion: maxVersion}
	srv.TLS.SetSessionTicketKeys([][32]byte{{1}})
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// ticketDir is a mailbox's base directory with the rotating keys' directory made,
// as a command's makeDirs makes it.
func ticketDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "keys"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// aProcess is what a new process has of a mailbox on srv: nothing in memory, and
// its default client trusting srv.
func aProcess(t *testing.T, dir string, srv *httptest.Server) *Mailbox {
	t.Helper()
	b, err := newMailbox(dir, Options{Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	Trust(b, pool)
	t.Cleanup(b.http.CloseIdleConnections)
	return b
}

// connects makes one new connection from b to its server, and reports whether
// it resumed TLS. A call that failed fails the test: no ticket fails a call.
func connects(t *testing.T, b *Mailbox) bool {
	t.Helper()
	resp, err := b.http.Get(b.base)
	if err != nil {
		t.Fatalf("a call failed: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body) // the ticket comes before the answer
	_ = resp.Body.Close()
	b.http.CloseIdleConnections()
	return resp.TLS.DidResume
}

func ticketPath(dir string) string { return filepath.Join(dir, "keys", ticketFile) }

// A server called by an IPv6 address keeps its ticket as any other does:
// crypto/tls keys a session by the host without its brackets.
func TestAnIPv6ServerKeepsItsTicket(t *testing.T) {
	t.Parallel()
	srv := ticketServerOn(t, "[::1]:0", 0)
	dir := ticketDir(t)
	if connects(t, aProcess(t, dir, srv)) {
		t.Fatal("the first connection a mailbox ever made resumed")
	}
	if !connects(t, aProcess(t, dir, srv)) {
		t.Fatalf("a new process on %s made a full handshake: no ticket was kept", srv.URL)
	}
}

// The test of a new process itself, in a process of its own, is
// TestASecondProcessResumesOnItsFirstConnection. This one shows what the base URL
// is for: a session cache alone resumes on a second port of the same host, so the
// control fails where the file does not name its URL.
func TestAChangedBaseURLDoesNotUseTheTicket(t *testing.T) {
	t.Parallel()
	here, there := ticketServer(t, 0), ticketServer(t, 0)

	t.Run("a session cache alone resumes on the other port", func(t *testing.T) {
		tr := defaultTransport(&holding{})
		pool := x509.NewCertPool()
		pool.AddCert(here.Certificate())
		tr.TLSClientConfig.RootCAs = pool
		c := &http.Client{Transport: tr}
		defer c.CloseIdleConnections()
		var resumed []bool
		for _, url := range []string{here.URL, there.URL} {
			resp, err := c.Get(url)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			resumed = append(resumed, resp.TLS.DidResume)
		}
		if resumed[0] || !resumed[1] {
			t.Fatalf("resumed %v, want a full handshake then a resumed one", resumed)
		}
	})

	dir := ticketDir(t)
	if connects(t, aProcess(t, dir, here)) {
		t.Fatal("the first connection a mailbox ever made resumed")
	}
	if !connects(t, aProcess(t, dir, here)) {
		t.Fatal("a new process on the same URL made a full handshake")
	}
	if connects(t, aProcess(t, dir, there)) {
		t.Fatal("a new process on another URL resumed with the old URL's ticket")
	}
	if !connects(t, aProcess(t, dir, there)) {
		t.Fatal("the new URL's ticket did not replace the old URL's")
	}
	// A read alone drops the ticket of another URL, with no connection after it.
	b := aProcess(t, dir, here)
	if cs, ok := b.http.Transport.(*transport).ticketCache().Get("127.0.0.1"); ok || cs != nil {
		t.Fatal("a process took another URL's ticket")
	}
	if _, err := os.Stat(ticketPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("another URL's ticket is still kept: %v", err)
	}
}

// No fault of the file fails a call: each costs one full handshake.
func TestATicketThatCannotBeReadOrWrittenFailsNoCall(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	for name, fault := range map[string]func(t *testing.T, dir string){
		"holds no session": func(t *testing.T, dir string) {
			if err := os.WriteFile(ticketPath(dir), []byte("not a session"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"is cut short": func(t *testing.T, dir string) {
			raw, err := os.ReadFile(ticketPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ticketPath(dir), raw[:len(raw)/2], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"is empty": func(t *testing.T, dir string) {
			if err := os.WriteFile(ticketPath(dir), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := ticketDir(t)
			connects(t, aProcess(t, dir, srv))
			fault(t, dir)
			if connects(t, aProcess(t, dir, srv)) {
				t.Fatal("resumed on a ticket that was no ticket")
			}
			if !connects(t, aProcess(t, dir, srv)) {
				t.Fatal("a new ticket did not replace the one that was no ticket")
			}
		})
	}
	t.Run("others can reach it", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			return // a mode says nothing there (perm_windows_test.go)
		}
		dir := ticketDir(t)
		connects(t, aProcess(t, dir, srv))
		if err := os.Chmod(ticketPath(dir), 0o644); err != nil {
			t.Fatal(err)
		}
		if connects(t, aProcess(t, dir, srv)) {
			t.Fatal("resumed on a ticket others can reach")
		}
		fi, err := os.Stat(ticketPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if m := fi.Mode().Perm(); m != 0o600 {
			t.Errorf("the ticket that replaced it has mode %v", m)
		}
	})
	t.Run("its directory is gone", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		b := aProcess(t, dir, srv)
		if connects(t, b) {
			t.Fatal("the first connection resumed")
		}
		if !connects(t, b) {
			t.Fatal("the process's memory did not resume its second connection")
		}
	})
	t.Run("a directory is in its place", func(t *testing.T) {
		t.Parallel()
		dir := ticketDir(t)
		if err := os.MkdirAll(filepath.Join(ticketPath(dir), "occupied"), 0o700); err != nil {
			t.Fatal(err)
		}
		b := aProcess(t, dir, srv)
		if connects(t, b) {
			t.Fatal("the first connection resumed")
		}
		if !connects(t, b) {
			t.Fatal("the process's memory did not resume its second connection")
		}
	})
}

// A TLS 1.2 session's master secret stays in memory. The control is the process's
// own second connection, which resumes.
func TestATLS12SessionIsNotKept(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, tls.VersionTLS12)
	dir := ticketDir(t)
	b := aProcess(t, dir, srv)
	if connects(t, b) {
		t.Fatal("the first connection resumed")
	}
	if !connects(t, b) {
		t.Fatal("the process's memory did not resume its second connection")
	}
	if _, err := os.Stat(ticketPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a TLS 1.2 session is on disk: %v", err)
	}
	if connects(t, aProcess(t, dir, srv)) {
		t.Fatal("a new process resumed a TLS 1.2 session")
	}
}

// The file holds the mailbox's server alone, and a nil session removes it.
func TestOneServerNameIsKept(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	dir := ticketDir(t)
	b := aProcess(t, dir, srv)
	connects(t, b)
	c := b.http.Transport.(*transport).ticketCache()
	cs := c.read()
	if cs == nil {
		t.Fatal("the file holds no session")
	}
	before, err := os.ReadFile(ticketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	c.Put("proxy.example", cs)
	after, err := os.ReadFile(ticketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("another server name's session went in the file")
	}
	if fresh := aProcess(t, dir, srv).http.Transport.(*transport).ticketCache(); fresh == nil {
		t.Fatal("no cache")
	} else if _, ok := fresh.Get("proxy.example"); ok {
		t.Error("another server name's Get read the file")
	} else if _, ok := fresh.Get("127.0.0.1"); !ok {
		t.Error("the server's Get did not read the file")
	}
	c.Put("127.0.0.1", nil)
	if _, err := os.Stat(ticketPath(dir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a nil session left the file: %v", err)
	}
}

// A mailbox with no directory, a Publish's or a Revoke's, has nowhere to keep a
// ticket, and a client given to a mailbox keeps its own.
func TestOnlyADefaultClientWithADirectoryKeepsATicket(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	nowhere, err := newMailbox("", Options{Base: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if nowhere.http.Transport.(*transport).ticketCache() != nil {
		t.Error("a mailbox with no directory keeps a ticket")
	}
	given, err := newMailbox(ticketDir(t), Options{Base: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if given.http.Transport.(*transport).ticketCache() != nil {
		t.Error("a client given to a mailbox keeps a ticket")
	}
}

// ticketClock is a clock a test moves, which connections read as they run.
type ticketClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *ticketClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *ticketClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

// aCertificate is a self-signed P-256 certificate for 127.0.0.1, with its key.
func aCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k, Leaf: leaf}
}

// tlsServer is a TLS server on 127.0.0.1 with cfg, which answers every request
// with name and counts it.
func tlsServer(t *testing.T, cfg *tls.Config, name string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		_, _ = io.WriteString(w, name)
	}))
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &n
}

// aProcessAt is a new process of a mailbox whose server is base, at clk, its
// default client trusting pool alone, and its connections dialled to addr where
// addr is not empty: a party on the network path.
func aProcessAt(t *testing.T, dir, base string, pool *x509.CertPool, clk *ticketClock, addr string) *Mailbox {
	t.Helper()
	b, err := newMailbox(dir, Options{Base: base, Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	Trust(b, pool)
	if addr != "" {
		(*b.http.Transport.(*transport).current()).(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
	}
	t.Cleanup(b.http.CloseIdleConnections)
	return b
}

// call makes one new connection from b to its server: the answer's body, and
// whether the connection resumed.
func call(b *Mailbox) (string, bool, error) {
	resp, err := b.http.Get(b.base)
	if err != nil {
		return "", false, err
	}
	body, _ := io.ReadAll(resp.Body) // the ticket comes before the answer
	_ = resp.Body.Close()
	b.http.CloseIdleConnections()
	return string(body), resp.TLS.DidResume, nil
}

// keptFull is the time of the last full handshake the ticket file in dir keeps,
// or zero.
func keptFull(t *testing.T, dir, base string) time.Time {
	t.Helper()
	c := newTicketCache(tls.NewLRUClientSessionCache(0), ticketPath(dir), base, time.Now)
	if c.read() == nil {
		return time.Time{}
	}
	return c.lastFull()
}

// serverSide is the session a server holding the ticket file's resumption
// secret resumes: the client's SessionState with its secret, read field by field
// from crypto/tls's encoding (crypto/tls/ticket.go), as a party who reads the
// file builds it.
func serverSide(t *testing.T, file string) *tls.SessionState {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("no ticket kept: %v", err)
	}
	s := cryptobyte.String(raw)
	var ver, suite uint16
	var typ uint8
	var created uint64
	var secret []byte
	if !s.ReadUint16(&ver) || !s.ReadUint8(&typ) || !s.ReadUint16(&suite) || !s.ReadUint64(&created) || !s.ReadUint8LengthPrefixed((*cryptobyte.String)(&secret)) {
		t.Fatal("the ticket file is not crypto/tls's encoding")
	}
	var b cryptobyte.Builder
	b.AddUint16(ver)
	b.AddUint8(1) // a server's
	b.AddUint16(suite)
	b.AddUint64(created)
	b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(secret) })
	b.AddUint24(0) // no extra
	b.AddUint8(0)  // no extended master secret
	b.AddUint8(0)  // no early data
	b.AddUint24(0) // no client certificates
	b.AddUint24(0) // no chains
	state, err := tls.ParseSessionState(b.BytesOrPanic())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// A party that reads tls-ticket and is on the network path resumes as the
// mailbox's server, with a certificate the client does not trust, and the
// client keeps the tickets it issues. The client resumes them only until
// resumeWithin after the last full handshake: a resumed connection never
// moves that time, so the bound ends the party's chain of tickets. The
// controls: with no ticket, the party's certificate is refused, and its chain
// still resumes just inside the bound.
func TestTheBoundEndsAChainOfTicketsAnImpostorIssued(t *testing.T) {
	t.Parallel()
	honestCert := aCertificate(t)
	honest, _ := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{honestCert}}, "honest")
	pool := x509.NewCertPool()
	pool.AddCert(honestCert.Leaf)
	t0 := time.Now().Truncate(time.Second)
	clk := &ticketClock{t: t0}
	dir := ticketDir(t)
	if _, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, "")); err != nil || resumed {
		t.Fatalf("the first call: resumed %v, %v", resumed, err)
	}
	if got := keptFull(t, dir, honest.URL); !got.Equal(t0) {
		t.Fatalf("the full handshake kept %v, want %v", got, t0)
	}

	stolen := serverSide(t, ticketPath(dir)) // a read: nothing is written
	impostorCert := aCertificate(t)
	keys := [][32]byte{{9}}
	first := &tls.Config{Certificates: []tls.Certificate{impostorCert},
		UnwrapSession: func([]byte, tls.ConnectionState) (*tls.SessionState, error) { return stolen, nil }}
	first.SetSessionTicketKeys(keys)
	impostor, _ := tlsServer(t, first, "impostor")
	addr := impostor.Listener.Addr().String()

	if _, _, err := call(aProcessAt(t, ticketDir(t), honest.URL, pool, clk, addr)); err == nil {
		t.Fatal("control: a full handshake took the impostor's certificate")
	}
	clk.set(t0.Add(time.Hour))
	if body, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, addr)); err != nil || !resumed || body != "impostor" {
		t.Fatalf("control: the read ticket did not resume to the impostor: %q, resumed %v, %v", body, resumed, err)
	}

	// A second party with only the first one's ticket keys resumes the tickets
	// it issued, which the client kept, until the bound.
	second := &tls.Config{Certificates: []tls.Certificate{impostorCert}}
	second.SetSessionTicketKeys(keys)
	chain, reached := tlsServer(t, second, "chain")
	addr = chain.Listener.Addr().String()
	clk.set(t0.Add(resumeWithin - time.Minute))
	if body, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, addr)); err != nil || !resumed || body != "chain" {
		t.Fatalf("control: inside the bound the impostor's ticket did not resume: %q, resumed %v, %v", body, resumed, err)
	}
	if got := keptFull(t, dir, honest.URL); !got.Equal(t0) {
		t.Fatalf("the impostor's resumptions moved the full handshake's time to %v, want %v", got, t0)
	}
	clk.set(t0.Add(resumeWithin + time.Second))
	before := reached.Load()
	if body, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, addr)); err == nil {
		t.Errorf("past the bound the impostor's chain of tickets answered: %q, resumed %v", body, resumed)
	}
	if reached.Load() != before {
		t.Error("past the bound a request reached the impostor")
	}
}

// A resumed connection does not move the time of the last full handshake, in
// the process that resumes or in the file: past the bound a session resumes
// nowhere, and the full handshake that follows moves the time.
func TestAResumptionDoesNotMoveTheFullHandshakesTime(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	t0 := time.Now().Truncate(time.Second)
	clk := &ticketClock{t: t0}
	dir := ticketDir(t)
	b := aProcessAt(t, dir, srv.URL, pool, clk, "")
	if _, resumed, err := call(b); err != nil || resumed {
		t.Fatalf("the first call: resumed %v, %v", resumed, err)
	}
	clk.set(t0.Add(resumeWithin - time.Hour))
	if _, resumed, err := call(aProcessAt(t, dir, srv.URL, pool, clk, "")); err != nil || !resumed {
		t.Fatalf("a new process inside the bound: resumed %v, %v", resumed, err)
	}
	if _, resumed, err := call(b); err != nil || !resumed {
		t.Fatalf("the first process inside the bound: resumed %v, %v", resumed, err)
	}
	if got := keptFull(t, dir, srv.URL); !got.Equal(t0) {
		t.Fatalf("a resumption moved the kept time to %v, want %v", got, t0)
	}
	past := t0.Add(resumeWithin + time.Second)
	clk.set(past)
	if _, resumed, err := call(b); err != nil || resumed {
		t.Fatalf("the first process past the bound: resumed %v, %v", resumed, err)
	}
	if _, resumed, err := call(aProcessAt(t, dir, srv.URL, pool, clk, "")); err != nil || !resumed {
		t.Fatalf("a new process after the full handshake: resumed %v, %v", resumed, err)
	}
	if got := keptFull(t, dir, srv.URL); !got.Equal(past) {
		t.Errorf("the full handshake past the bound kept %v, want %v", got, past)
	}
}

// A full handshake that does not complete moves no time: a party holding the
// server's certificate and not its key gets past the certificate's chain, and
// its CertificateVerify is refused.
func TestAFullHandshakeThatDoesNotCompleteMovesNoTime(t *testing.T) {
	t.Parallel()
	honestCert := aCertificate(t)
	honest, _ := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{honestCert}}, "honest")
	pool := x509.NewCertPool()
	pool.AddCert(honestCert.Leaf)
	t0 := time.Now().Truncate(time.Second)
	clk := &ticketClock{t: t0}
	dir := ticketDir(t)
	if _, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, "")); err != nil || resumed {
		t.Fatalf("the first call: resumed %v, %v", resumed, err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	copied := tls.Certificate{Certificate: honestCert.Certificate, PrivateKey: other}
	impostor, _ := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{copied}}, "impostor")
	clk.set(t0.Add(resumeWithin - time.Hour))
	b := aProcessAt(t, dir, honest.URL, pool, clk, impostor.Listener.Addr().String())
	if _, _, err := call(b); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("a party without the server's key: %v; want its signature refused", err)
	}
	if got := b.http.Transport.(*transport).tickets.lastFull(); !got.Equal(t0) {
		t.Fatalf("a full handshake that did not complete moved the time to %v, want %v", got, t0)
	}
	clk.set(t0.Add(resumeWithin + time.Second))
	if _, resumed, err := call(aProcessAt(t, dir, honest.URL, pool, clk, "")); err != nil || resumed {
		t.Errorf("past the bound of the last full handshake that completed: resumed %v, %v", resumed, err)
	}
}

// A ticket file that keeps no time of a full handshake gives no session, and the
// next full handshake writes one.
func TestATicketThatKeepsNoTimeGivesNoSession(t *testing.T) {
	t.Parallel()
	srv := ticketServer(t, 0)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	clk := &ticketClock{t: time.Now().Truncate(time.Second)}
	dir := ticketDir(t)
	if _, _, err := call(aProcessAt(t, dir, srv.URL, pool, clk, "")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ticketPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	state, err := tls.ParseSessionState(raw)
	if err != nil {
		t.Fatal(err)
	}
	state.Extra = slices.DeleteFunc(state.Extra, func(e []byte) bool { return bytes.HasPrefix(e, []byte(extraFull)) })
	if raw, err = state.Bytes(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ticketPath(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, resumed, err := call(aProcessAt(t, dir, srv.URL, pool, clk, "")); err != nil || resumed {
		t.Fatalf("a ticket that keeps no time: resumed %v, %v", resumed, err)
	}
	if _, resumed, err := call(aProcessAt(t, dir, srv.URL, pool, clk, "")); err != nil || !resumed {
		t.Fatalf("the ticket the full handshake wrote: resumed %v, %v", resumed, err)
	}
}
