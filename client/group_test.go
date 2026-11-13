package client_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

// reporting answers every send as the server does, with Dromas-RateLimit
// set to field on its 201 and 200, or none for "".
func reporting(e *env, field string) {
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPost && field != "" {
			w = &withField{ResponseWriter: w, field: field}
		}
		next.ServeHTTP(w, r)
	})
}

type withField struct {
	http.ResponseWriter
	field string
}

func (w *withField) WriteHeader(code int) {
	if code == http.StatusCreated || code == http.StatusOK {
		w.Header().Set(protocol.RateLimitField, w.field)
	}
	w.ResponseWriter.WriteHeader(code)
}

// A message to a group is one entry to each other member, with one msg_id,
// and each member is delivered it with the whole group, sorted (PROTOCOL.md
// §9.1, §12.2).
func TestAGroupMessageGoesToEachOtherMember(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, c := e.mailbox(), e.mailbox(), e.mailbox()
	fl, err := a.SendGroup(ctx, []protocol.Address{b.Address(), c.Address(), a.Address(), b.Address()}, "to the group", client.Fields{Subject: "plans"})
	if err != nil || len(fl.Sent) != 2 || len(fl.Queued) != 0 {
		t.Fatalf("the group send: %+v, %v; want two sent", fl, err)
	}
	if fl.Sent[0].MsgID != fl.Sent[1].MsgID {
		t.Fatalf("the members' messages have msg_ids %s and %s, want one", fl.Sent[0].MsgID, fl.Sent[1].MsgID)
	}
	want := []protocol.Address{a.Address(), b.Address(), c.Address()}
	slices.SortFunc(want, func(x, y protocol.Address) int { return cmpText(x, y) })
	for _, m := range []*client.Mailbox{b, c} {
		res := receive(t, m)
		if len(res.Delivered) != 1 {
			t.Fatalf("%s: delivered %d", m.Address(), len(res.Delivered))
		}
		env := res.Delivered[0].Envelope
		if env.To != m.Address() || env.MsgID != fl.Sent[0].MsgID || !slices.Equal(env.Group, want) || env.Subject != "plans" {
			t.Errorf("%s: delivered to %s, msg_id %s, group %v, subject %q", m.Address(), env.To, env.MsgID, env.Group, env.Subject)
		}
	}
	if _, err := a.SendGroup(ctx, []protocol.Address{a.Address()}, "to nobody", client.Fields{}); err == nil {
		t.Error("a group of this mailbox alone was sent")
	}
}

func cmpText(x, y protocol.Address) int {
	switch xs, ys := x.String(), y.String(); {
	case xs < ys:
		return -1
	case xs > ys:
		return 1
	}
	return 0
}

// The members' entries go as one unit by the budget the server reports:
// where they do not fit, they wait together, untried, until they do, and
// the follow loop posts them then; a group larger than the budget ever
// holds is refused with nothing written; an answer with no field leaves no
// estimate, and the group goes.
func TestAGroupWaitsForItsBudgetAsOneUnit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, c, d := e.mailbox(), e.mailbox(), e.mailbox(), e.mailbox()
	// One back each second, three at most; after this send, one left and
	// full in two seconds.
	reporting(e, "sends=1;c=3;q=86400;f=2")
	send(t, a, d, "spends the budget")
	e.srv.SetMiddle(nil)
	fl, err := a.SendGroup(ctx, []protocol.Address{b.Address(), c.Address()}, "waits a second", client.Fields{})
	if err != nil || len(fl.Sent) != 0 || len(fl.Queued) != 2 {
		t.Fatalf("a group of two with one send left: %+v, %v; want both queued", fl, err)
	}
	due := e.clock.Now().Add(time.Second)
	for _, q := range fl.Queued {
		if q.At.Before(due) || q.At.After(due.Add(time.Second)) {
			t.Errorf("queued for %s at %s, want %s", q.To, q.At, due)
		}
	}
	o := oneDrain(false)
	o.Flush = true
	sent := 0
	follow := func() {
		t.Helper()
		if err := a.Follow(ctx, o, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventSent {
				sent++
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	follow()
	if sent != 0 {
		t.Fatalf("the loop before the budget allows sent %d", sent)
	}
	e.clock.Add(time.Second)
	follow()
	if sent != 2 {
		t.Fatalf("the loop once the budget allows sent %d, want both", sent)
	}

	reporting(e, "sends=0;c=1;q=86400;f=1")
	send(t, a, d, "a budget of one")
	e.srv.SetMiddle(nil)
	if _, err := a.SendGroup(ctx, []protocol.Address{b.Address(), c.Address()}, "never fits", client.Fields{}); err == nil {
		t.Fatal("a group of two past a budget of one was taken")
	}
	if n, _ := filepath.Glob(filepath.Join(a.Dir(), "outbox", "*.P")); len(n) != 0 {
		t.Fatalf("%d entries written for a group refused", len(n))
	}

	reporting(e, "")
	send(t, a, d, "an answer with no field")
	if fl, err := a.SendGroup(ctx, []protocol.Address{b.Address(), c.Address()}, "goes", client.Fields{}); err != nil || len(fl.Sent) != 2 {
		t.Fatalf("a group after an answer with no field: %+v, %v; want both sent", fl, err)
	}
}
