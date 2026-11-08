package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
