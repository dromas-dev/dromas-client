package client_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// lookups records, for each read of a mailbox or of an address's revocations,
// whether it carried a pass.
func lookups(e *env) (seen func() map[string]bool) {
	var mu sync.Mutex
	got := map[string]bool{}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		_, read := forge.ReadOf(r)
		if _, revocations := forge.RevocationsOf(r); read || revocations {
			mu.Lock()
			got[r.URL.Path] = r.Header.Get("Authorization") != ""
			mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
	return func() map[string]bool {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]bool{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

// A mailbox's reads carry no pass (PROTOCOL.md §11.8, §11.15, §11.5): the
// reads are public, and a server reads none on them. Not the recipient's
// mailbox before a send, not a sender's revocations in a drain, and not a
// mailbox's read of its own.
func TestLookupsCarryNoPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	seen := lookups(e)
	send(t, a, b, "asked about with no pass")
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("delivered %d", len(res.Delivered))
	}
	got := seen()
	for _, p := range []string{mailboxPath(b), mailboxPath(a) + "/revocation"} {
		if carried, asked := got[p]; !asked || carried {
			t.Errorf("GET %s: asked %v, with a pass %v", p, asked, carried)
		}
	}
	seen = lookups(e)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if carried, asked := seen()[mailboxPath(b)]; !asked || carried {
		t.Errorf("a mailbox's read of its own mailbox: asked %v, with a pass %v", asked, carried)
	}
}

// A server that holds an address revoked answers 410 to whatever is sent for
// it, with no check. A revocation published there again is taken as done
// once the record it then asks for verifies, and is not otherwise.
func TestARevocationPublishedToARevokedMailbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a := e.mailbox()
	rec := revoke(t, e, a)
	if _, err := client.Revoke(ctx, e.opts(), rec); err != nil {
		t.Fatalf("published again to a server that holds it: %v", err)
	}
	says(e, protocol.CodeMailboxRevoked, http.MethodPut, "/revocation")
	b := e.mailbox()
	other, err := b.Revocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Revoke(ctx, e.opts(), other); err == nil {
		t.Fatal("a 410 from a server that holds no revocation was taken as done")
	}
}

// readLog records each read of a mailbox's own path: the If-None-Match it
// sent and the status it was answered.
type readLog struct {
	mu   sync.Mutex
	sent []string
	got  []int
}

func (l *readLog) take() ([]string, []int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, g := l.sent, l.got
	l.sent, l.got = nil, nil
	return s, g
}

// statusOf is a ResponseWriter that remembers the status it was given.
type statusOf struct {
	http.ResponseWriter
	code int
}

func (s *statusOf) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// logReads logs each read of b's mailbox, answered by the server, or by
// answer where it gives an answer of its own.
func logReads(e *env, b *client.Mailbox, answer func(w http.ResponseWriter, r *http.Request) bool) *readLog {
	l := &readLog{}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method != http.MethodGet || r.URL.Path != mailboxPath(b) {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusOf{ResponseWriter: w, code: http.StatusOK}
		if answer == nil || !answer(sw, r) {
			next.ServeHTTP(sw, r)
		}
		l.mu.Lock()
		l.sent, l.got = append(l.sent, r.Header.Get("If-None-Match")), append(l.got, sw.code)
		l.mu.Unlock()
	})
	return l
}

// A send's read of its recipient is conditional on the answer it kept
// (PROTOCOL.md §12.2): the first asks no condition, the next sends that
// answer's tag and is answered 304, and the message goes as on a 200.
func TestASendsReadIsConditionalOnTheAnswerKept(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	l := logReads(e, b, nil)
	send(t, a, b, "first")
	send(t, a, b, "second")
	sent, got := l.take()
	if len(sent) != 2 || sent[0] != "" || got[0] != http.StatusOK || !strings.HasPrefix(sent[1], `W/"`) || got[1] != http.StatusNotModified {
		t.Fatalf("the two reads sent %q and were answered %v, want none then a tag, 200 then 304", sent, got)
	}
	if res := receive(t, b); len(res.Delivered) != 2 {
		t.Fatalf("delivered %d of 2", len(res.Delivered))
	}
}

// A 304 about another tag than the one the read sent is no answer: the
// message waits in the outbox, and goes at the next flush.
func TestA304AboutAnotherTagHoldsTheMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "first")
	l := logReads(e, b, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("If-None-Match") == "" {
			return false
		}
		w.Header().Set("ETag", `W/"another"`)
		w.WriteHeader(http.StatusNotModified)
		return true
	})
	if _, err := a.Send(ctx, b.Address(), "held"); err == nil || !strings.Contains(err.Error(), "kept in the outbox") {
		t.Fatalf("a send answered a 304 about another tag: %v, want it kept in the outbox", err)
	}
	if n, _ := waiting(a, b.Address()); n != 1 || failed(a, b.Address()) != 0 {
		t.Fatalf("%d waiting, %d failed, want 1 and 0", n, failed(a, b.Address()))
	}
	l.take()
	logReads(e, b, nil)
	if _, err := a.Flush(ctx); err != nil {
		t.Fatalf("the flush after: %v", err)
	}
	if res := receive(t, b); len(res.Delivered) != 2 {
		t.Fatalf("delivered %d of 2", len(res.Delivered))
	}
}

// On a 304 the kept answer is checked again, at that time: a prekey past
// its not_after since is refused, as on a 200.
func TestAKeptAnswerIsCheckedAgainOnA304(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "first")
	l := logReads(e, b, nil)
	e.clock.Add(protocol.MaxPrekeyLifetime + time.Hour)
	_, err := a.Send(ctx, b.Address(), "after its prekey ended")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("a send whose kept answer's prekey has ended: %v, want it refused as expired", err)
	}
	if _, got := l.take(); len(got) != 1 || got[0] != http.StatusNotModified {
		t.Fatalf("the read was answered %v, want a 304", got)
	}
}

// After a 409 prekey_superseded, the next read of the recipient asks no
// condition (PROTOCOL.md §12.2).
func TestAReadAfterPrekeySupersededAsksNoCondition(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "first")
	posts := 0
	l := &readLog{}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == mailboxPath(b):
			l.mu.Lock()
			l.sent = append(l.sent, r.Header.Get("If-None-Match"))
			l.mu.Unlock()
		case r.Method == http.MethodPost && r.URL.Path == postTo(b):
			if posts++; posts == 1 {
				forge.Refuse(w, protocol.CodePrekeySuperseded)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
	send(t, a, b, "second")
	if sent, _ := l.take(); len(sent) != 2 || sent[0] == "" || sent[1] != "" {
		t.Fatalf("the reads sent %q, want a tag, then none after the 409", sent)
	}
}
