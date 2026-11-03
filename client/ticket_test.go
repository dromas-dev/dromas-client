package client_test

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// ticketChild is the environment variable a copy of this binary reads to run one
// recv of a mailbox on the default client.
const ticketChild = "DROMAS_TEST_TICKET"

// ticketOrder is what the copy runs: the mailbox's directory, its server's URL and
// certificate, and its clock in Unix seconds.
type ticketOrder struct {
	Dir, Base   string
	Certificate []byte
	Now         int64
}

// TestTicketChild is the copy of this binary that runs one recv and exits: in a
// test run of its own it does nothing.
func TestTicketChild(t *testing.T) {
	raw := os.Getenv(ticketChild)
	if raw == "" {
		return
	}
	var o ticketOrder
	if err := json.Unmarshal([]byte(raw), &o); err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(o.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	now := time.Unix(o.Now, 0).UTC()
	b, err := client.Open(o.Dir, client.Options{Base: o.Base, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	client.Trust(b, pool)
	if _, err := b.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestASecondProcessResumesOnItsFirstConnection runs recv in two real processes,
// each of which holds nothing in memory. The second resumes TLS on its first
// connection, by the server's own record of the connection, and its pass is taken
// on that connection. The control removes the file, and a third process makes a
// full handshake.
func TestASecondProcessResumesOnItsFirstConnection(t *testing.T) {
	t.Parallel()
	e := &env{t: t, clock: &clock{t: time.Now().UTC().Truncate(time.Second)}}
	e.srv = fakeserver.NewTLS(t, e.clock.Now)
	e.http = e.srv.Client()
	b := e.mailbox()
	file := b.Path("tls-ticket")
	if _, err := os.Stat(file); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a client given to the mailbox kept a ticket: %v", err)
	}

	var mu sync.Mutex
	var resumed []bool
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		mu.Lock()
		resumed = append(resumed, r.TLS.DidResume)
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
	// firstConnection runs one process, and reports whether its first request
	// came on a resumed connection.
	firstConnection := func() bool {
		t.Helper()
		mu.Lock()
		resumed = nil
		mu.Unlock()
		order, err := json.Marshal(ticketOrder{Dir: b.Dir(), Base: e.srv.URL, Certificate: e.srv.Certificate().Raw, Now: e.clock.Now().Unix()})
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(os.Args[0], "-test.run=^TestTicketChild$", "-test.count=1") //nolint:gosec // this test binary, again
		cmd.Env = append(os.Environ(), ticketChild+"="+string(order))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("recv in a process of its own: %v\n%s", err, out)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(resumed) == 0 {
			t.Fatal("the process made no request")
		}
		return resumed[0]
	}

	if firstConnection() {
		t.Fatal("the first process resumed, with no ticket kept")
	}
	if err := ownerOnly(file); err != nil {
		t.Fatalf("the ticket: %v", err)
	}
	if !firstConnection() {
		t.Fatal("the second process made a full handshake")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if firstConnection() {
		t.Fatal("a process with no ticket kept resumed")
	}
}
