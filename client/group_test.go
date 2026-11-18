package client_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

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

// Each member's entry is posted on its own: a refusal about the sender at
// the second post leaves that member's entry and the one after it waiting
// its Retry-After, the later one untried, and the member posted first is
// kept in sent/ and held once by the server. The follow loop once the wait
// is over posts the two that waited and nothing twice.
func TestAGroupsRefusalAboutTheSenderLeavesOnlyTheUnpostedWaiting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b, c, d := e.mailbox(), e.mailbox(), e.mailbox(), e.mailbox()
	rp := refusing(e, "60", map[string]protocol.Code{postTo(c): protocol.CodeRateLimited})
	fl, err := a.SendGroup(ctx, []protocol.Address{b.Address(), c.Address(), d.Address()}, "to the group", client.Fields{})
	if client.Code(err) != protocol.CodeRateLimited || fl == nil || len(fl.Sent) != 1 || fl.Sent[0].To != b.Address() {
		t.Fatalf("the group send: %+v, %v; want the first member sent and rate_limited", fl, err)
	}
	if len(fl.Queued) != 1 || fl.Queued[0].To != d.Address() || fl.Queued[0].At.Before(e.clock.Now().Add(60*time.Second)) {
		t.Fatalf("queued %+v, want the member after the refusal waiting 60 seconds", fl.Queued)
	}
	if rp.count(postTo(c)) != 1 || rp.count(postTo(d)) != 0 {
		t.Fatalf("posts to the refused member %d and to the one after it %d, want 1 and 0", rp.count(postTo(c)), rp.count(postTo(d)))
	}
	kept := a.Path("sent", b.Address().String()+"."+fl.Sent[0].MsgID+".json")
	if !exists(kept) || e.srv.Count(b.Address()) != 1 {
		t.Fatalf("the member posted: kept in sent/ %v, held by the server %d times; want kept, and held once", exists(kept), e.srv.Count(b.Address()))
	}

	rp = refusing(e, "60", nil)
	o := oneDrain(false)
	o.Flush = true
	var sent []protocol.Address
	follow := func() {
		t.Helper()
		if err := a.Follow(ctx, o, func(_ context.Context, ev client.Event) error {
			if ev.Kind == client.EventSent {
				sent = append(sent, ev.Sent.To)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	e.clock.Add(59 * time.Second)
	follow()
	if len(sent) != 0 {
		t.Fatalf("the loop before the wait is over sent to %v", sent)
	}
	e.clock.Add(time.Second)
	follow()
	want := []protocol.Address{c.Address(), d.Address()}
	slices.SortFunc(want, cmpText)
	slices.SortFunc(sent, cmpText)
	if !slices.Equal(sent, want) || rp.count(postTo(b)) != 0 {
		t.Fatalf("the loop once due sent to %v and posted to the member already sent %d times; want %v and none", sent, rp.count(postTo(b)), want)
	}
	for _, m := range []*client.Mailbox{b, c, d} {
		if n := e.srv.Count(m.Address()); n != 1 {
			t.Errorf("%s: the server holds %d, want 1", m.Address(), n)
		}
	}
}
