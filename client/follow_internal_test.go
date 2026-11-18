package client

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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

// A kick that came while the webhook's handover was under way, with Follow's
// loop ended before that handover did, is handed over before run returns
// (#927 item 9), whichever of the two run's select takes first. The order is
// made by the handler, never timed: it holds the first handover until the
// second message is owed, kicked, and the loop ended. Records owed for
// messages inbox/ does not keep are handed over as EventOwedGone, so the test
// needs no delivery.
func TestAKickDuringTheLastHandoverIsHandedOverBeforeRunReturns(t *testing.T) {
	t.Parallel()
	b, _, _ := twoOpens(t)
	ctx := context.Background()
	record := func(id string) {
		t.Helper()
		if err := b.lockedFinishedDo(ctx, func() error {
			return b.update(func(s store) error { _, err := owe(s, webhookRecords, id); return err })
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, second := strings.Repeat("a", 64), strings.Repeat("b", 64) // ids of kept messages' form
	record(first)
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
	record(second)
	w.kick()
	close(w.done)
	close(release)
	if err := <-ended; err != nil || !slices.Equal(handed, []string{first, second}) {
		t.Errorf("run: %v, having handed over %q", err, handed)
	}
}
