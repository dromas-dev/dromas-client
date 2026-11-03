package client_test

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

// A message sealed to prekey n, a renewal to n+1, and n's file removed by hand:
// recv stops with nothing acked, the server still holds the message, and status
// says the same. With the file back, the message is delivered.
func TestARetiredPrekeyMissingStopsRecv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "sealed to prekey n")
	n := numberOf(t, b)
	e.clock.Add(2 * time.Second)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	file := prekeyFile(b, n, ".agekey")
	key, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	res, err := b.Receive(ctx)
	if err == nil || res != nil {
		t.Fatalf("recv with prekey %d missing: %+v, %v", n, res, err)
	}
	for _, word := range []string{strconv.FormatUint(n, 10), file, "dromas prekey renew", "lost"} {
		if !strings.Contains(err.Error(), word) {
			t.Errorf("the refusal does not say %q: %v", word, err)
		}
	}
	if got := e.srv.Count(b.Address()); got != 1 {
		t.Fatalf("the server holds %d, and nothing was to be acked", got)
	}
	st, serr := b.Status(ctx)
	if serr != nil || !slices.Contains(st.Attention, err.Error()) {
		t.Errorf("status: %q, %v", st.Attention, serr)
	}
	if err := os.WriteFile(file, key, 0o600); err != nil {
		t.Fatal(err)
	}
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("recv with its key back: %+v", res)
	}
}

// A key the mailbox deleted does not stop a drain, by either rule that deletes
// one.
func TestAPrunedPrekeyDoesNotStopADrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, wait := range map[string]time.Duration{
		"an emptying drain": 2 * time.Hour,
		"its deletion time": protocol.PrekeyLifetime + protocol.TTL + time.Minute,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			n := numberOf(t, b)
			e.clock.Add(2 * time.Second)
			if _, err := b.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
			e.clock.Add(wait)
			receive(t, b)
			if exists(prekeyFile(b, n, ".agekey")) {
				t.Fatalf("prekey %d was not deleted", n)
			}
			send(t, a, b, "after")
			if res := receive(t, b); len(res.Delivered) != 1 {
				t.Fatalf("the drain after the deletion: %+v", res)
			}
		})
	}
}

// An upload the server took, whose answer the mailbox never had, reads in the state
// file as a statement never published, and senders seal to it: so its key missing
// stops recv too, and the message stays on the server.
func TestAStatementTakenUnseenWhoseKeyIsMissingStopsRecv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "renewal taken", protocol.Address{})
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	unseen := servedSerial(t, e, x)
	if numberOf(t, x) == unseen {
		t.Fatal("the mailbox has seen the renewal: no test of an unseen one")
	}
	send(t, a, x, "sealed to the statement the mailbox has not seen taken")
	if err := os.Remove(prekeyFile(x, unseen, ".agekey")); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Receive(ctx); err == nil || !strings.Contains(err.Error(), strconv.FormatUint(unseen, 10)) {
		t.Fatalf("recv: %v", err)
	}
	if got := e.srv.Count(x.Address()); got != 1 {
		t.Fatalf("the server holds %d, and nothing was to be acked", got)
	}
}

// prekey renew, its owner's recovery, leaves no record that stops a later drain:
// recv then refuses and acks what was sealed to the missing key, as the refusal
// said, and goes on.
func TestARenewalForgetsAPrekeyMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "sealed to prekey n")
	n := numberOf(t, b)
	e.clock.Add(2 * time.Second)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(prekeyFile(b, n, ".agekey")); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(2 * time.Second)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(client.Statements(t, b), n) {
		t.Fatalf("the renewal kept the record of prekey %d", n)
	}
	res := receive(t, b)
	if len(res.Refused) != 1 || e.srv.Count(b.Address()) != 0 {
		t.Fatalf("recv after the renewal: %+v; the server holds %d", res, e.srv.Count(b.Address()))
	}
	send(t, a, b, "after")
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the drain after that: %+v", res)
	}
}

// A renewal the server does not take forgets nothing: recv goes on stopping, and
// the owner has lost nothing by trying.
func TestARenewalNotTakenForgetsNoPrekeyMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "sealed to prekey n")
	n := numberOf(t, b)
	e.clock.Add(2 * time.Second)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(prekeyFile(b, n, ".agekey")); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(2 * time.Second)
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/prekey") {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
	if _, err := b.Rekey(ctx); err == nil {
		t.Fatal("a renewal the server refused was taken")
	}
	e.srv.SetMiddle(nil)
	if _, err := b.Receive(ctx); err == nil || !strings.Contains(err.Error(), strconv.FormatUint(n, 10)) {
		t.Fatalf("recv: %v", err)
	}
	if got := e.srv.Count(b.Address()); got != 1 {
		t.Fatalf("the server holds %d, and nothing was to be acked", got)
	}
}

// An adopted key has no statement to mark or to read as lost: its file gone by hand
// stops no drain, and the next prune past its time drops the adoption.
func TestAnAdoptedKeyMissingStopsNoDrain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	old := numberOf(t, b)
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	took := e.clock.Now()
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(prekeyFile(b, old, ".agekey")); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Receive(ctx); err != nil {
		t.Fatalf("recv with an adopted key's file gone: %v", err)
	}
	e.clock.Add(adoptedEnd(took).Add(time.Minute).Sub(e.clock.Now()))
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 {
		t.Fatalf("still adopted: %v", got)
	}
}
