package client

import (
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/protocol"
)

// The estimate's arithmetic is PROTOCOL.md §11.5's: the spec's example
// leaves 37; k sends fit once the bucket holds k; a kept estimate projected
// through a post stands above a lower reading, as a lagging server process
// reads; and one of another size is not projected.
func TestTheBudgetEstimatesAsTheServerCounts(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	r := protocol.Budget{Left: 37, Cap: 60, Refill: 50, Window: protocol.DefaultWindow, Full: 39000}
	g := nextBudget(nil, r, true, now)
	if got := g.left(now); got != 37 {
		t.Fatalf("left %d, want 37 as the reading says", got)
	}
	if at, ok := g.due(37, now); !ok || !at.Equal(now) {
		t.Errorf("37 due %v %v, want now", at, ok)
	}
	if at, ok := g.due(40, now); !ok || !at.Equal(g.FullAt.Add(-20*g.Interval)) || !at.After(now) {
		t.Errorf("40 due %v %v, want when 20 intervals remain", at, ok)
	}
	if _, ok := g.due(61, now); ok {
		t.Error("61 sends fit a budget of 60")
	}
	if got := g.left(g.FullAt); got != 60 {
		t.Errorf("left %d once full, want 60", got)
	}

	lagging := r
	lagging.Full = 20000
	next := nextBudget(&g, lagging, true, now)
	if want := g.FullAt.Add(g.Interval); !next.FullAt.Equal(want) {
		t.Errorf("after a lower reading the full time is %s, want the kept one projected through the post, %s", next.FullAt, want)
	}
	if dup := nextBudget(&g, lagging, false, now); !dup.FullAt.Equal(g.FullAt) {
		t.Errorf("a duplicate's answer moved the full time to %s, want %s: it spends nothing", dup.FullAt, g.FullAt)
	}
	other := r
	other.Cap, other.Full = 10, 100
	if o := nextBudget(&g, other, true, now); !o.FullAt.Equal(now.Add(100 * time.Second)) {
		t.Errorf("a reading of another size gives %s, want the reading's own", o.FullAt)
	}
}
