package client

import (
	"context"
	"errors"
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// handOver hands over one event for each thing a drain did, of every kind a
// drain has, in the order the kinds are declared, whatever the handler returns,
// and returns its first error.
func TestHandOverHandsOverEveryKindInItsOrder(t *testing.T) {
	t.Parallel()
	res := &Result{
		Delivered: []Delivery{{ID: "d"}}, Duplicates: []string{"u"}, Refused: []Refusal{{ID: "r", BeforeRestore: true}},
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
