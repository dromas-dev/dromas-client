package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// The header's value and the secret's bytes, which no line may carry.
const (
	hookToken  = "Bearer hook-token-sekrit"
	hookSecret = "whsec_c2VrcmV0LXNla3JpdC1zZWtyaXQtc2VrcmV0LTEyMzQ1Ng=="
	hookPath   = "/hook-path-sekrit"
)

// endpoint is a webhook's endpoint that answers each post as answer says, and
// keeps each post it was sent, with when it came.
type endpoint struct {
	mu     sync.Mutex
	posts  []post
	answer func(n int, w http.ResponseWriter)
}

type post struct {
	at     time.Time
	method string
	path   string
	header http.Header
	body   []byte
}

func (p *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	p.posts = append(p.posts, post{time.Now(), r.Method, r.URL.Path, r.Header.Clone(), body})
	n := len(p.posts)
	p.mu.Unlock()
	if p.answer != nil {
		p.answer(n, w)
	}
}

func (p *endpoint) got() []post {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.posts)
}

// answering is an endpoint that answers every post with status.
func answering(status int) *endpoint {
	return &endpoint{answer: func(_ int, w http.ResponseWriter) { w.WriteHeader(status) }}
}

// hookFiles writes a header file and a secret file, its owner's alone, and
// returns the flags that name them.
func hookFiles(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	header, secret := privateFile(t, dir, "Authorization: "+hookToken+"\n"), privateFile(t, dir, hookSecret+"\n")
	return []string{"--webhook-header-file", header, "--webhook-secret-file", secret}
}

// privateFile writes body to a new file in dir, its owner's alone from its
// making on every system (client.NewPrivateTemp), and returns its name.
func privateFile(t *testing.T, dir, body string) string {
	t.Helper()
	f, err := client.NewPrivateTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

// quickHook makes a test's tries no wait apart: their count is its business.
func quickHook(t *testing.T) {
	t.Helper()
	was := webhookDelays
	webhookDelays = [webhookTries - 1]time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { webhookDelays = was })
}

// Each post is the message line recv prints, as JSON with no line feed, with the
// owner's header and Standard Webhooks' three headers: webhook-id the
// message's id, and a signature that verifies under the secret.
func TestTheWebhookPostsTheLineWithTheHeaderAndTheSignature(t *testing.T) {
	c := newRecvEnv(t)
	hook := answering(http.StatusNoContent)
	srv := httptest.NewServer(hook)
	defer srv.Close()
	c.send("to the endpoint")
	stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s", "--webhook", srv.URL + hookPath}, hookFiles(t)...)...)
	if err != nil {
		t.Fatalf("recv: %v %s", err, stderr)
	}
	ms, posts := ofType(t, stdout, "message"), hook.got()
	if len(ms) != 1 || len(posts) != 1 {
		t.Fatalf("recv printed %s; %d posts", stdout, len(posts))
	}
	p := posts[0]
	if p.method != http.MethodPost || p.path != hookPath || bytes.HasSuffix(p.body, []byte("\n")) || untimedText(string(p.body))+"\n" != stdout {
		t.Fatalf("posted %s %s %q, recv printed %q", p.method, p.path, p.body, stdout)
	}
	id := ms[0]["id"].(string)
	ts := p.header.Get("webhook-timestamp")
	mac := hmac.New(sha256.New, mustB64(t, strings.TrimPrefix(hookSecret, "whsec_")))
	fmt.Fprintf(mac, "%s.%s.%s", id, ts, p.body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if p.header.Get("Content-Type") != "application/json" || p.header.Get("Authorization") != hookToken || p.header.Get("webhook-id") != id || p.header.Get("webhook-signature") != want {
		t.Fatalf("headers %v, want the signature %s", p.header, want)
	}
	if n, err := strconv.ParseInt(ts, 10, 64); err != nil || time.Since(time.Unix(n, 0)).Abs() > time.Minute {
		t.Fatalf("webhook-timestamp %q", ts)
	}
	if !strings.HasPrefix(p.header.Get("User-Agent"), "dromas/") {
		t.Fatalf("User-Agent %q", p.header.Get("User-Agent"))
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --webhook beside --exec: where the program fails and the endpoint answers,
// and where the endpoint fails and the program runs, each fails alone, with
// its own lines and its own record, and recv exits with status 3.
func TestTheWebhookAndExecFailApart(t *testing.T) {
	quickHook(t)
	for _, failing := range []string{"exec", "webhook"} {
		t.Run(failing, func(t *testing.T) {
			c := newCallback(t)
			hook, code := answering(http.StatusOK), "0"
			if failing == "webhook" {
				hook = answering(http.StatusInternalServerError)
			} else {
				code = "7"
			}
			srv := httptest.NewServer(hook)
			defer srv.Close()
			c.send("one", "two")
			stdout, stderr, err := c.recv(context.Background(), append([]string{"--wait", "0s", "--webhook", srv.URL}, c.exec("exit", code)...)...)
			if status := report(err, io.Discard); status != 3 {
				t.Fatalf("recv: exit %d, %v; %s", status, err, stderr)
			}
			runs, posts := strings.Count(c.recorded(), "\n"), len(hook.got())
			st, err := c.b.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			switch failing {
			case "exec":
				if runs != 6 || posts != 2 || len(ofType(t, stdout, "exec_failed")) != 2 || len(ofType(t, stdout, "webhook_attempt_failed")) != 0 || len(st.GaveUp) != 2 || len(st.WebhookGaveUp) != 0 {
					t.Fatalf("%d runs, %d posts; %s; %+v", runs, posts, stdout, st)
				}
			case "webhook":
				tries := ofType(t, stdout, "webhook_attempt_failed")
				if runs != 2 || posts != 6 || len(tries) != 6 || len(ofType(t, stdout, "webhook_failed")) != 2 || len(ofType(t, stdout, "exec_attempt_failed")) != 0 || len(st.GaveUp) != 0 || len(st.WebhookGaveUp) != 2 {
					t.Fatalf("%d runs, %d posts; %s; %+v", runs, posts, stdout, st)
				}
				for i, l := range tries {
					if l["attempt"] != jsonInt(i%3+1) || l["status"] != jsonInt(500) {
						t.Errorf("try %d: %v", i, l)
					}
				}
			}
			if st.Owed != 0 || st.WebhookOwed != 0 {
				t.Fatalf("owed after: %+v", st)
			}
		})
	}
}

// A redirect is an answer that failed, and is never followed.
func TestTheWebhookFollowsNoRedirect(t *testing.T) {
	quickHook(t)
	c := newRecvEnv(t)
	there := answering(http.StatusOK)
	target := httptest.NewServer(there)
	defer target.Close()
	srv := httptest.NewServer(&endpoint{answer: func(_ int, w http.ResponseWriter) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}})
	defer srv.Close()
	c.send("redirected")
	stdout, _, err := c.recv(context.Background(), "--wait", "0s", "--webhook", srv.URL)
	tries := ofType(t, stdout, "webhook_attempt_failed")
	if report(err, io.Discard) != 3 || len(tries) != 3 || tries[0]["status"] != jsonInt(307) || len(there.got()) != 0 {
		t.Fatalf("recv: %v; %s; %d posts followed", err, stdout, len(there.got()))
	}
}

// The tries are at once, then 5 seconds and 5 minutes later; a longer
// Retry-After waits longer, up to 5 minutes, and a shorter one waits the
// schedule's time.
func TestTheWebhooksTriesAreAtOnceThen5sThen5m(t *testing.T) {
	for name, tc := range map[string]struct {
		retryAfter string
		want       []time.Duration
	}{
		"none":        {"", []time.Duration{0, 5 * time.Second, 5*time.Minute + 5*time.Second}},
		"longer":      {"60", []time.Duration{0, time.Minute, 6 * time.Minute}},
		"past 5m":     {"3600", []time.Duration{0, 5 * time.Minute, 10 * time.Minute}},
		"shorter":     {"1", []time.Duration{0, 5 * time.Second, 5*time.Minute + 5*time.Second}},
		"a date, 90s": {"date", []time.Duration{0, 90 * time.Second, 6*time.Minute + 30*time.Second}},
	} {
		t.Run(name, func(t *testing.T) {
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
				if _, err := a.Send(ctx, b.Address(), "retried"); err != nil {
					t.Fatal(err)
				}
				hook := &endpoint{answer: func(_ int, w http.ResponseWriter) {
					v := tc.retryAfter
					if v == "date" {
						v = time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
					}
					if v != "" {
						w.Header().Set("Retry-After", v)
					}
					w.WriteHeader(http.StatusServiceUnavailable)
				}}
				ep := httptest.NewTestServer(t, hook)
				was := webhookClient
				webhookClient = ep.Client()
				t.Cleanup(func() { webhookClient = was })
				useMailbox(t, b.Dir())
				t.Setenv("DROMAS_SERVER_URL", srv.URL)
				var out, errs bytes.Buffer
				err = doWith(ctx, opt, "recv", []string{"--wait", "0s", "--webhook", "http://127.0.0.1/hook"}, strings.NewReader(""), &out, &errs)
				if report(err, io.Discard) != 3 {
					t.Fatalf("recv: %v %s", err, errs.String())
				}
				posts := hook.got()
				var at []time.Duration
				for _, p := range posts {
					at = append(at, p.at.Sub(posts[0].at))
				}
				if !slices.Equal(at, tc.want) {
					t.Fatalf("posts at %v, want %v", at, tc.want)
				}
			})
		})
	}
}

// No line recv prints carries the header, the secret or the URL: the line
// types are every one webhook.go makes, read from its source, each made once at
// least, by an endpoint that answers 500, one that is not there, and a message
// owed that inbox/ no longer keeps.
func TestNoWebhookLineCarriesItsSecrets(t *testing.T) {
	quickHook(t)
	types := lineTypesOf(t, "webhook.go")
	if len(types) < 3 {
		t.Fatalf("webhook.go makes the lines %v", types)
	}
	c := newRecvEnv(t)
	flags := hookFiles(t)
	var all strings.Builder
	run := func(url string) {
		t.Helper()
		stdout, stderr, _ := c.recv(context.Background(), append([]string{"--wait", "0s", "--webhook", url}, flags...)...)
		all.WriteString(stdout + stderr)
	}
	failing := httptest.NewServer(answering(http.StatusInternalServerError))
	defer failing.Close()
	c.send("answered 500")
	run(failing.URL + hookPath)
	gone := httptest.NewServer(answering(http.StatusOK))
	gone.Close()
	c.send("to nobody")
	run(gone.URL + hookPath)
	// A message left owed, then gone from inbox/.
	c.send("deleted")
	cut := func(context.Context, client.Event) error { return errors.New("cut short") }
	if err := c.b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Webhook: cut}, nil); err != nil {
		t.Fatal(err)
	}
	ids, _ := filepath.Glob(filepath.Join(c.b.Dir(), "mail", "inbox", "*.json"))
	for _, id := range ids {
		if err := os.Remove(id); err != nil {
			t.Fatal(err)
		}
	}
	run(failing.URL + hookPath)
	out := all.String()
	for _, typ := range types {
		if len(ofType(t, out, typ)) == 0 {
			t.Errorf("no %s line was made: %s", typ, out)
		}
	}
	if len(ofType(t, out, "webhook_attempt_failed")) == 0 || !strings.Contains(out, `"failure":"could not connect"`) {
		t.Errorf("no try failed for want of a connection: %s", out)
	}
	for _, secret := range []string{hookToken, "hook-token", "sekrit", strings.TrimPrefix(hookSecret, "whsec_"), hookPath, failing.URL} {
		if strings.Contains(out, secret) {
			t.Errorf("a line carries %q: %s", secret, out)
		}
	}
}

// lineTypesOf is every line type that file's calls of line make.
func lineTypesOf(t *testing.T, file string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, isID := c.Fun.(*ast.Ident); isID && id.Name == "line" && len(c.Args) > 0 {
			if lit, ok := c.Args[0].(*ast.BasicLit); ok {
				types = append(types, strings.Trim(lit.Value, `"`))
			}
		}
		return true
	})
	return types
}

// A wrong --webhook, or a file of its that others can read or that holds no
// header or secret, runs nothing, with exit status 2, and its words quote
// neither the URL's user nor what the file holds.
func TestAWrongWebhookRunsNothing(t *testing.T) {
	c := newRecvEnv(t)
	c.send("waits on the server")
	dir := t.TempDir()
	file := func(_, body string, mode os.FileMode) string {
		p := privateFile(t, dir, body)
		if mode != 0o600 {
			if err := os.Chmod(p, mode); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	cases := map[string][]string{
		"no wait":           {"--webhook", "http://127.0.0.1/x"},
		"a password":        {"--wait", "0s", "--webhook", "http://me:sekrit@127.0.0.1/x"},
		"not http":          {"--wait", "0s", "--webhook", "ftp://127.0.0.1/x"},
		"a file and no URL": {"--wait", "0s", "--webhook-header-file", file("h", "A: sekrit", 0o600)},
		"a header of ours":  {"--wait", "0s", "--webhook", "http://127.0.0.1/x", "--webhook-header-file", file("ct", "Content-Type: sekrit", 0o600)},
		"no secret's form":  {"--wait", "0s", "--webhook", "http://127.0.0.1/x", "--webhook-secret-file", file("s", "sekrit", 0o600)},
	}
	if os.PathSeparator == '/' {
		cases["a header others read"] = []string{"--wait", "0s", "--webhook", "http://127.0.0.1/x", "--webhook-header-file", file("open", "Authorization: sekrit", 0o644)}
	}
	for name, args := range cases {
		stdout, stderr, err := c.recv(context.Background(), args...)
		if status := report(err, io.Discard); status != 2 || stdout != "" || strings.Contains(stderr+fmt.Sprint(err), "sekrit") {
			t.Errorf("%s: exit %d, %v; %q %q", name, status, err, stdout, stderr)
		}
	}
	if st, err := c.b.Status(context.Background()); err != nil || st.Inbox != 0 {
		t.Fatalf("something ran: %+v, %v", st, err)
	}
}

// The config file's [recv] webhook and its two files serve a recv that waits,
// where the command line names none; a file key without the URL is a
// config_error.
func TestTheConfigFilesWebhookServesARecvThatWaits(t *testing.T) {
	c := newRecvEnv(t)
	hook := answering(http.StatusOK)
	srv := httptest.NewServer(hook)
	defer srv.Close()
	flags := hookFiles(t)
	dir := c.b.Dir()
	base := "base-dir = " + tomlString(dir) + "\nmail-dir = " + tomlString(filepath.Join(dir, "mail")) + "\n[recv]\n"
	t.Setenv("DROMAS_CONFIG", writeConfig(t, base+"webhook = "+tomlString(srv.URL)+"\nwebhook-header-file = "+tomlString(flags[1])+"\nwebhook-secret-file = "+tomlString(flags[3])+"\n"))
	c.send("one")
	if _, stderr, err := c.recv(context.Background()); err != nil || len(hook.got()) != 0 {
		t.Fatalf("a recv that does not wait: %v %s; %d posts", err, stderr, len(hook.got()))
	}
	c.send("two")
	if _, stderr, err := c.recv(context.Background(), "--wait", "0s"); err != nil || len(hook.got()) != 1 || hook.got()[0].header.Get("Authorization") != hookToken || hook.got()[0].header.Get("webhook-signature") == "" {
		t.Fatalf("recv --wait: %v %s; %v", err, stderr, hook.got())
	}
	t.Setenv("DROMAS_CONFIG", writeConfig(t, base+"webhook-header-file = "+tomlString(flags[1])+"\n"))
	_, stderr, err := c.recv(context.Background(), "--wait", "0s")
	if _, ok := errors.AsType[*configError](err); report(err, io.Discard) != 2 || !ok {
		t.Fatalf("a header file without a webhook: %v %s", err, stderr)
	}
}
