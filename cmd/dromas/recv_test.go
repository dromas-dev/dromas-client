package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
	"github.com/dromas-dev/dromas-client/protocol"
)

// recvEnv is a server, a mailbox a that sends and a mailbox b whose recv a test
// runs, the commands' environment set to b.
type recvEnv struct {
	t    *testing.T
	srv  *fakeserver.Server
	a, b *client.Mailbox
}

func newRecvEnv(t *testing.T) *recvEnv {
	t.Helper()
	ctx := context.Background()
	srv := fakeserver.New(t, time.Now)
	opt := client.Options{Base: srv.URL}
	a, err := client.Init(ctx, t.TempDir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	b, err := client.Init(ctx, t.TempDir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	allows(t, b, a)
	useMailbox(t, b.Dir())
	t.Setenv("DROMAS_SERVER_URL", srv.URL)
	return &recvEnv{t: t, srv: srv, a: a, b: b}
}

func (c *recvEnv) send(bodies ...string) {
	c.t.Helper()
	for _, body := range bodies {
		if _, err := c.a.Send(context.Background(), c.b.Address(), body); err != nil {
			c.t.Fatal(err)
		}
	}
}

// recv runs recv with args.
func (c *recvEnv) recv(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	var out, errs bytes.Buffer
	err = do(ctx, "recv", args, strings.NewReader(""), &out, &errs)
	return out.String(), errs.String(), err
}

// ofType is the printed lines of a type, in their order.
func ofType(t *testing.T, stdout, typ string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range printed(t, stdout) {
		if l["type"] == typ {
			out = append(out, l)
		}
	}
	return out
}

// recv --follow goes on after each message, past the point recv --wait ends, and
// ends at --wait's deadline where both are given, with exit status 0.
func TestRecvFollowGoesOnToItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		srv := fakeserver.NewInMemory(t, time.Now)
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client()}
		a, err := client.Init(ctx, t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		b, err := client.Init(ctx, t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		allows(t, b, a)
		sent := make(chan struct{})
		t.Cleanup(func() { <-sent })
		go func() {
			defer close(sent)
			for _, body := range []string{"the first", "the second"} {
				time.Sleep(2 * time.Minute)
				_, _ = a.Send(ctx, b.Address(), body)
			}
		}()
		useMailbox(t, b.Dir())
		t.Setenv("DROMAS_SERVER_URL", srv.URL)
		var out, errs bytes.Buffer
		start := time.Now()
		if err := doWith(ctx, opt, "recv", []string{"--follow", "--wait", "10m"}, strings.NewReader(""), &out, &errs); err != nil {
			t.Fatalf("recv --follow: %v %s", err, errs.String())
		}
		if took := time.Since(start); len(ofType(t, out.String(), "message")) != 2 || took != 10*time.Minute {
			t.Fatalf("recv --follow --wait 10m: %q after %s; %s", out.String(), took, errs.String())
		}
	})
}

// recv --follow reads the mailbox's names at each drain, since it runs for longer
// than one name stays as it is.
func TestRecvFollowReadsNamesAtEachDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		srv := fakeserver.NewInMemory(t, time.Now)
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client()}
		a, err := client.Init(ctx, t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		b, err := client.Init(ctx, t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		allows(t, b, a)
		other, err := client.Open(b.Dir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		sent := make(chan struct{})
		t.Cleanup(func() { <-sent })
		go func() {
			defer close(sent)
			time.Sleep(2 * time.Minute)
			_, _ = a.Send(ctx, b.Address(), "before its name")
			time.Sleep(2 * time.Minute)
			_ = other.SetAlias(ctx, "alice", a.Address())
			time.Sleep(2 * time.Minute)
			_, _ = a.Send(ctx, b.Address(), "after its name")
		}()
		useMailbox(t, b.Dir())
		t.Setenv("DROMAS_SERVER_URL", srv.URL)
		var out, errs bytes.Buffer
		if err := doWith(ctx, opt, "recv", []string{"--follow", "--wait", "10m"}, strings.NewReader(""), &out, &errs); err != nil {
			t.Fatalf("recv --follow: %v %s", err, errs.String())
		}
		ms := ofType(t, out.String(), "message")
		if len(ms) != 2 || ms[0]["alias"] != nil || ms[1]["alias"] != "alice" {
			t.Fatalf("recv --follow printed %s", out.String())
		}
	})
}

// recv --follow rides out what a wait mends, saying each on stderr, with no end
// of its own, and ends with no error when its context ends.
func TestRecvFollowRidesOutAServerErrorAndEndsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		srv := fakeserver.NewInMemory(t, time.Now)
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client()}
		b, err := client.Init(ctx, t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		var failed atomic.Int32
		mine := "/v1/" + b.Address().String() + "/messages"
		srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
			if r.Method == http.MethodGet && r.URL.Path == mine && failed.Add(1) <= 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
		useMailbox(t, b.Dir())
		t.Setenv("DROMAS_SERVER_URL", srv.URL)
		var out, errs bytes.Buffer
		ended := make(chan error, 1)
		go func() { ended <- doWith(ctx, opt, "recv", []string{"--follow"}, strings.NewReader(""), &out, &errs) }()
		time.Sleep(time.Hour)
		synctest.Wait()
		cancel()
		if err := <-ended; err != nil {
			t.Fatalf("recv --follow, its context ended: %v %s", err, errs.String())
		}
		if n := strings.Count(errs.String(), `"for":"server"`); n != 3 {
			t.Fatalf("%d waiting lines, want 3: %s", n, errs.String())
		}
	})
}

// A refusal no wait mends ends recv with exit status 4 and "fatal" in its "error"
// line, however it was run; a failure a restart may pass gets 1, with no "fatal".
func TestRecvTellsAFatalEndFromOneToRetry(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		code   string
		fatal  bool
		args   []string
	}{
		{"no mailbox under recv --follow", http.StatusNotFound, string(protocol.CodeNoSuchMailbox), true, []string{"--follow"}},
		{"bad request under recv --wait", http.StatusBadRequest, string(protocol.CodeBadRequest), true, []string{"--wait", "1m"}},
		{"bad request under recv", http.StatusBadRequest, string(protocol.CodeBadRequest), true, nil},
		{"a server error at recv's deadline", http.StatusServiceUnavailable, string(protocol.CodeUnavailable), false, []string{"--wait", "1s"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newRecvEnv(t)
			mine := "/v1/" + e.b.Address().String()
			e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
				if strings.HasPrefix(r.URL.Path, mine) && r.Method != http.MethodGet || r.URL.Path == mine+"/messages" {
					fakeserver.WriteJSON(w, c.status, protocol.Problem{Type: protocol.Code(c.code).TypeURI(), Code: protocol.Code(c.code)})
					return
				}
				next.ServeHTTP(w, r)
			})
			_, _, err := e.recv(context.Background(), c.args...)
			var errs bytes.Buffer
			status := report(err, &errs)
			want := 1
			if c.fatal {
				want = 4
			}
			line := printed(t, errs.String())
			if status != want || len(line) != 1 || line[0]["type"] != "error" || (line[0]["fatal"] == true) != c.fatal {
				t.Fatalf("exit %d, %s; err %v", status, errs.String(), err)
			}
		})
	}
	e := newRecvEnv(t)
	rec, err := e.b.Revocation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.b.KeepRevocation(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.recv(context.Background(), "--follow"); report(err, io.Discard) != 4 {
		t.Fatalf("a revoked mailbox's recv --follow: %v", err)
	}
}

// A server that forgot the mailbox, and that answers its registration again
// 503, ends recv with exit status 1 and no "fatal": the next recv registers it
// (#773).
func TestRecvOfAForgottenMailboxWhoseRegistrationFailsForNowExits1(t *testing.T) {
	e := newRecvEnv(t)
	e.srv.Forget(e.b.Address())
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method != http.MethodGet {
			fakeserver.WriteJSON(w, http.StatusServiceUnavailable, protocol.Problem{Type: protocol.CodeUnavailable.TypeURI(), Code: protocol.CodeUnavailable})
			return
		}
		next.ServeHTTP(w, r)
	})
	_, _, err := e.recv(context.Background())
	var errs bytes.Buffer
	status := report(err, &errs)
	line := printed(t, errs.String())
	if status != 1 || len(line) != 1 || line[0]["type"] != "error" || line[0]["fatal"] != nil || line[0]["code"] != string(protocol.CodeNoSuchMailbox) {
		t.Fatalf("exit %d, %s; err %v", status, errs.String(), err)
	}
}

// recv --follow rides out a folder missing, as on a volume not yet mounted,
// with an "error" line each hourly try and no "fatal", and so does recv
// --follow --exec, which hands over what is owed first; a bare recv ends on it
// with exit status 1 (#775 item 2, #801).
func TestRecvRidesOutAFolderNotYetMounted(t *testing.T) {
	prog, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1)")
	}
	synctest.Test(t, func(t *testing.T) {
		srv := fakeserver.NewInMemory(t, time.Now)
		opt := client.Options{Base: srv.URL, HTTPClient: srv.Client()}
		b, err := client.Init(context.Background(), t.TempDir(), opt)
		if err != nil {
			t.Fatal(err)
		}
		inbox := b.Path("inbox")
		if err := os.Rename(inbox, inbox+".unmounted"); err != nil {
			t.Fatal(err)
		}
		useMailbox(t, b.Dir())
		t.Setenv("DROMAS_SERVER_URL", srv.URL)
		var out, errs bytes.Buffer
		err = doWith(context.Background(), opt, "recv", nil, strings.NewReader(""), &out, &errs)
		var rep bytes.Buffer
		if st := report(err, &rep); st != 1 || strings.Contains(rep.String(), `"fatal"`) || !strings.Contains(rep.String(), "may not be mounted") {
			t.Fatalf("a bare recv: exit %d, %s", st, rep.String())
		}
		for _, args := range [][]string{{"--follow"}, {"--follow", "--exec", prog}} {
			errs.Reset()
			ctx, cancel := context.WithCancel(context.Background())
			ended := make(chan error, 1)
			go func() { ended <- doWith(ctx, opt, "recv", args, strings.NewReader(""), &out, &errs) }()
			time.Sleep(150 * time.Minute)
			synctest.Wait()
			cancel()
			if err := <-ended; err != nil {
				t.Fatalf("recv %v, its context ended: %v %s", args, err, errs.String())
			}
			lines := printed(t, errs.String())
			if len(lines) != 3 {
				t.Fatalf("recv %v: %d lines on stderr, want 3: %s", args, len(lines), errs.String())
			}
			for _, l := range lines {
				if l["type"] != "error" || l["fatal"] != nil || !strings.Contains(l["detail"].(string), inbox) {
					t.Fatalf("recv %v said %v", args, l)
				}
			}
		}
	})
}
