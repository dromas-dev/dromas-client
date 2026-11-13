package client_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// refusePosts answers each post to a mailbox in refuse's set with its code,
// a Retry-After of wait seconds, and, for rate_limited, the sender's sends
// as its quota; it counts every post made.
type refusePosts struct {
	mu     sync.Mutex
	refuse map[string]protocol.Code
	posts  map[string]int
}

func refusing(e *env, wait string, refuse map[string]protocol.Code) *refusePosts {
	rp := &refusePosts{refuse: refuse, posts: map[string]int{}}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		rp.mu.Lock()
		rp.posts[r.URL.Path]++
		c, ok := rp.refuse[r.URL.Path]
		rp.mu.Unlock()
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", wait)
		forge.Refuse(w, c, func(p *protocol.Problem) {
			if c == protocol.CodeRateLimited {
				p.QuotaID, p.Subject = protocol.QuotaSends, protocol.SubjectMailbox
			}
		})
	})
	return rp
}

func (rp *refusePosts) count(path string) int {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return rp.posts[path]
}

// A post refused with a Retry-After leaves its entry waiting that long: the
// follow loop's flush before then posts nothing, one after posts it, and a
// flush by hand posts it at once.
func TestARefusalsRetryAfterIsKeptForTheEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	follow := func(b *client.Mailbox) []*client.Sent {
		t.Helper()
		var sent []*client.Sent
		o := oneDrain(false)
		o.Flush = true
		if err := b.Follow(ctx, o, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventSent {
				sent = append(sent, ev.Sent)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return sent
	}
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	rp := refusing(e, "120", map[string]protocol.Code{postTo(b): protocol.CodeRateLimited})
	if _, err := a.Send(ctx, b.Address(), "waits two minutes"); client.Code(err) != protocol.CodeRateLimited {
		t.Fatalf("the send: %v, want rate_limited", err)
	}
	e.srv.SetMiddle(nil)
	e.clock.Add(119 * time.Second)
	if sent := follow(a); len(sent) != 0 || rp.count(postTo(b)) != 1 {
		t.Fatalf("the loop a second before the wait sent %d, %d posts in all; want nothing more", len(sent), rp.count(postTo(b)))
	}
	e.clock.Add(time.Second)
	if sent := follow(a); len(sent) != 1 {
		t.Fatalf("the loop at the wait sent %d, want 1", len(sent))
	}

	// By hand, at once.
	rp = refusing(e, "3600", map[string]protocol.Code{postTo(b): protocol.CodeRateLimited})
	if _, err := a.Send(ctx, b.Address(), "waits an hour"); client.Code(err) != protocol.CodeRateLimited {
		t.Fatalf("the second send: %v, want rate_limited", err)
	}
	e.srv.SetMiddle(nil)
	if fl, err := a.FlushReport(ctx); err != nil || len(fl.Sent) != 1 || len(fl.Queued) != 0 {
		t.Fatalf("a flush by hand: %+v, %v; want it sent at once", fl, err)
	}
	if res := receive(t, b); len(res.Delivered) != 2 {
		t.Fatalf("delivered %d of 2", len(res.Delivered))
	}
}

// A refusal about the sender (its own sends) stops the flush: every entry
// after it waits as long, untried. One about the recipient alone
// (mailbox_full) leaves that entry waiting, and the flush goes on.
func TestWhoARefusalIsAboutDecidesWhatTheFlushTries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct {
		code      protocol.Code
		triedNext bool
	}{
		{protocol.CodeRateLimited, false},
		{protocol.CodeUnavailable, false},
		{protocol.CodeMailboxFull, true},
	} {
		e := newEnv(t)
		a, b, d := e.mailbox(), e.mailbox(), e.mailbox()
		// Two entries waiting, posted in the order of their names.
		first, second := b, d
		if d.Address().String() < b.Address().String() {
			first, second = d, b
		}
		refusing(e, "60", map[string]protocol.Code{postTo(first): protocol.CodeUnavailable, postTo(second): protocol.CodeUnavailable})
		for _, to := range []*client.Mailbox{first, second} {
			if _, err := a.Send(ctx, to.Address(), "waiting"); err == nil {
				t.Fatalf("%s: the send went", c.code)
			}
		}
		e.clock.Add(61 * time.Second)
		rp := refusing(e, "300", map[string]protocol.Code{postTo(first): c.code})
		fl, err := a.FlushReport(ctx)
		if err == nil || client.Code(err) != c.code {
			t.Fatalf("%s: the flush: %v", c.code, err)
		}
		if got := rp.count(postTo(second)) == 1; got != c.triedNext {
			t.Errorf("%s: the next entry tried %v, want %v", c.code, got, c.triedNext)
		}
		if !c.triedNext && (len(fl.Queued) != 1 || fl.Queued[0].To != second.Address() || fl.Queued[0].At.Before(e.clock.Now().Add(300*time.Second))) {
			t.Errorf("%s: queued %+v, want the next entry waiting 300 seconds", c.code, fl.Queued)
		}
		if c.triedNext && len(fl.Sent) != 1 {
			t.Errorf("%s: sent %d, want the next entry", c.code, len(fl.Sent))
		}
	}
}

// Under FollowOptions.Flush the loop posts what is due after its drain, and
// hands each over as an EventSent.
func TestFollowFlushesTheOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	refusing(e, "0", map[string]protocol.Code{postTo(b): protocol.CodeUnavailable})
	if _, err := a.Send(ctx, b.Address(), "posted by the loop"); err == nil {
		t.Fatal("the send went")
	}
	e.srv.SetMiddle(nil)
	var sent []*client.Sent
	o := oneDrain(false)
	o.Flush = true
	if err := a.Follow(ctx, o, func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventSent {
			sent = append(sent, ev.Sent)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].To != b.Address() {
		t.Fatalf("the loop sent %+v, want the one waiting", sent)
	}
	if n, _ := waiting(a, b.Address()); n != 0 {
		t.Fatalf("%d still waiting", n)
	}
}
