//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package client

import (
	"context"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// A copy of the state file and of the gate that outlives their close, as a
// process that another goroutine starts holds one from its fork to its exec,
// makes no later open of this program wait: a flock is the open file's, so
// closeState and the gate's closing let go of it first (unflock, flock(2)).
// recv --exec's program, started while recv's webhook read the mailbox, made
// recv say once that it waited for its own lock (#991). The copies here are
// dup(2)'s, the same open file a fork copies, kept open to the test's end.
func TestACopyOfTheStateFileHeldPastItsCloseMakesNoOpenWait(t *testing.T) {
	savedPatience, savedCopied := lockPatience, copiedHere
	lockPatience = time.Second // a wait for a copy outlasts it, and is refused
	var copies []int
	t.Cleanup(func() {
		lockPatience, copiedHere = savedPatience, savedCopied
		for _, fd := range copies {
			_ = syscall.Close(fd)
		}
	})
	a, _, _ := twoOpens(t)
	copiedHere = func(state, gate *os.File) {
		for _, f := range []*os.File{state, gate} {
			if f == nil {
				continue
			}
			fd, err := syscall.Dup(int(f.Fd())) //nolint:gosec // a descriptor fits an int
			if err != nil {
				t.Error(err)
				continue
			}
			copies = append(copies, fd)
		}
	}
	var told atomic.Int32
	a.waiting = func() { told.Add(1) }
	ctx := context.Background()
	if _, err := a.Aliases(ctx); err != nil {
		t.Fatalf("the reading whose files are copied: %v", err)
	}
	if len(copies) != 2 {
		t.Fatalf("%d copies, want the state file's and the gate's", len(copies))
	}
	copiedHere = nil
	if err := a.lockedFinishedDo(ctx, func() error { return nil }); err != nil {
		t.Errorf("a command after it: %v", err)
	}
	if _, err := a.Aliases(ctx); err != nil {
		t.Errorf("a reading after it: %v", err)
	}
	if n := told.Load(); n != 0 {
		t.Errorf("Waiting was told %d times, with no other command of the mailbox running", n)
	}
}
