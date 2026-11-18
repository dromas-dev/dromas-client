package client

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// handOver hands over one event for each thing a drain did, of every kind a
// drain has, in the order the kinds are declared, whatever the handler returns,
// and returns its first error.
func TestHandOverHandsOverEveryKindInItsOrder(t *testing.T) {
	t.Parallel()
	res := &Result{
		Delivered: []Delivery{{ID: "d"}}, Duplicates: []string{"u"}, Refused: []Refusal{{ID: "r"}},
		Held: []Delivery{{ID: "h"}}, Blocked: []string{"b"}, More: true,
		Renewed: &protocol.Prekey{}, RenewErr: errors.New("renew"), NotMade: &NotMade{},
		ACLErr: errors.New("acl"), ACLReplaced: true,
	}
	first := errors.New("first")
	var kinds []EventKind
	err, fault := (&Mailbox{}).handOver(context.Background(), false, res, func(_ context.Context, ev Event) error {
		kinds = append(kinds, ev.Kind)
		if ev.Result != res {
			t.Errorf("event %d carries no Result", ev.Kind)
		}
		if len(kinds) == 1 {
			return first
		}
		return errors.New("later")
	})
	if !errors.Is(err, first) || fault != nil {
		t.Errorf("handOver: %v, %v; want the first error and no fault", err, fault)
	}
	if len(kinds) != int(EventDrained) {
		t.Fatalf("%d events, want %d: %v", len(kinds), EventDrained, kinds)
	}
	for i, k := range kinds {
		if k != EventKind(i)+EventDelivery {
			t.Errorf("event %d is kind %d", i, k)
		}
	}
}

// owedAs records the message id as owed in r, as a delivery's transaction
// does (owe). A record for a message inbox/ does not keep is handed over as
// EventOwedGone, so a test of the handover needs no delivery; id need only
// have a kept message's form, 64 hex.
func owedAs(t *testing.T, b *Mailbox, r records, id string) {
	t.Helper()
	if err := b.lockedFinishedDo(context.Background(), func() error {
		return b.update(func(s store) error { _, err := owe(s, r, id); return err })
	}); err != nil {
		t.Fatal(err)
	}
}

// A kick that came while the webhook's handover was under way, with Follow's
// loop ended before that handover did, is handed over before run returns
// (#927 item 9), whichever of the two run's select takes first: so the
// scenario runs 20 times, and a run that drops the kick at the end fails one
// with near certainty. The order is made by the handler, never timed: it
// holds the first handover until the second message is owed, kicked, and the
// loop ended.
func TestAKickDuringTheLastHandoverIsHandedOverBeforeRunReturns(t *testing.T) {
	t.Parallel()
	b, _, _ := twoOpens(t)
	ctx := context.Background()
	for i := range 20 {
		first, second := fmt.Sprintf("%064x", 2*i+1), fmt.Sprintf("%064x", 2*i+2)
		owedAs(t, b, webhookRecords, first)
		begun, release := make(chan struct{}), make(chan struct{})
		var handed []string
		w := &webhooks{b: b, kicked: make(chan struct{}, 1), done: make(chan struct{}), h: func(_ context.Context, ev Event) error {
			handed = append(handed, ev.ID)
			if ev.ID == first {
				close(begun)
				<-release
			}
			return nil
		}}
		ended := make(chan error, 1)
		go func() { ended <- w.run(ctx) }()
		<-begun
		owedAs(t, b, webhookRecords, second)
		w.kick()
		close(w.done)
		close(release)
		if err := <-ended; err != nil || !slices.Equal(handed, []string{first, second}) {
			t.Fatalf("run %d: %v, having handed over %q", i, err, handed)
		}
	}
}

// A handover that stops at a message its handler's error left owed is tried
// again webhookRetry after it, as one a fault cut short is, with no kick: the
// messages owed behind that one wait no longer than that (#934 review, item
// 3). In a bubble, on its fake clock.
func TestAWebhookHandoverStoppedAtAMessageLeftOwedIsTriedAgain(t *testing.T) {
	t.Parallel()
	a, _, _ := twoOpens(t)
	synctest.Test(t, func(t *testing.T) {
		b, err := Open(a.Dir(), Options{Base: a.base})
		if err != nil {
			t.Fatal(err)
		}
		first, second := strings.Repeat("a", 64), strings.Repeat("b", 64)
		owedAs(t, b, webhookRecords, first)
		owedAs(t, b, webhookRecords, second)
		var handed []string
		w := &webhooks{b: b, kicked: make(chan struct{}, 1), done: make(chan struct{}), h: func(_ context.Context, ev Event) error {
			handed = append(handed, ev.ID)
			if len(handed) == 1 {
				return errors.New("not yet")
			}
			return nil
		}}
		ended := make(chan error, 1)
		go func() { ended <- w.run(context.Background()) }()
		synctest.Wait()
		if !slices.Equal(handed, []string{first}) {
			t.Fatalf("the first handover handed over %q, want the first message alone", handed)
		}
		time.Sleep(webhookRetry)
		synctest.Wait()
		close(w.done)
		if err := <-ended; err != nil || !slices.Equal(handed, []string{first, first, second}) {
			t.Errorf("run: %v, having handed over %q", err, handed)
		}
	})
}
