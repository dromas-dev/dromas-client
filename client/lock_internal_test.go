package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/internal/fakeserver"
)

// twoOpens is one mailbox opened twice, as two processes would: a, for a
// command, and r, for a reading, each a Mailbox with a slot of its own, and the
// path of their state file.
func twoOpens(t *testing.T) (a, r *Mailbox, state string) {
	t.Helper()
	srv := fakeserver.New(t, time.Now)
	opt := Options{Base: srv.URL}
	dir := t.TempDir()
	if _, err := Init(context.Background(), dir, opt); err != nil {
		t.Fatal(err)
	}
	var err error
	if a, err = Open(dir, opt); err != nil {
		t.Fatal(err)
	}
	if r, err = Open(dir, opt); err != nil {
		t.Fatal(err)
	}
	return a, r, filepath.Join(dir, "state")
}

// A reading whose open finds no state file reads none at every view, and
// opens none that a command makes after that open: Status's views, behind a
// command that made the file again and holds it, would each wait for that
// command with no context, up to lockPatience (#382). What the reading waited
// on is what its Mailbox is told (Options.Waiting), which here also lets the
// command go, so a reading that waits is seen at once and on no clock (#509).
func TestAReadingThatFoundNoStateFileWaitsForNoCommand(t *testing.T) {
	t.Parallel()
	a, r, state := twoOpens(t)
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	holding, release, cmd := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var waited atomic.Bool
	var letGo sync.Once
	r.waiting = func() {
		waited.Store(true)
		letGo.Do(func() { close(release) })
	}
	cmdDone := false
	var cmdErr error
	_, err := reading(context.Background(), r, func() (*Status, error) {
		// Once the reading's open has found no file, a command makes it
		// again and holds it.
		go func() {
			cmd <- a.lockedFinishedDo(context.Background(), func() error {
				close(holding)
				<-release
				return nil
			})
		}()
		select {
		case <-holding:
		case cmdErr = <-cmd:
			cmdDone = true
			return nil, errors.New("the command ended without holding the mailbox")
		}
		return r.status()
	})
	letGo.Do(func() { close(release) })
	if !cmdDone {
		cmdErr = <-cmd
	}
	if cmdErr != nil {
		t.Errorf("the command: %v", cmdErr)
	}
	if waited.Load() || err != nil {
		t.Errorf("a reading behind a command that made the state file after the reading's open: waited for it %v, %v", waited.Load(), err)
	}
}

// One reading, two views, a command's write between them, on a mailbox whose
// state file was gone at the reading's open: both views read one state, the
// one with no file (#329, #382).
func TestAReadingThatFoundNoStateFileReadsOneState(t *testing.T) {
	t.Parallel()
	a, r, state := twoOpens(t)
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		var v []byte
		if err := r.view(func(s store) error { v = s.get(bucketMailbox, "written"); return nil }); err != nil {
			return "error: " + err.Error()
		}
		return string(v)
	}
	done := make(chan error, 1)
	waited := false
	views, _ := reading(context.Background(), r, func() ([2]string, error) {
		first := read()
		go func() {
			done <- a.lockedFinishedDo(context.Background(), func() error {
				return a.update(func(s store) error { return s.put(bucketMailbox, "written", []byte("written by a command")) })
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("the command: %v", err)
			}
		case <-time.After(time.Second):
			waited = true // the command waits for the reading
		}
		return [2]string{first, read()}, nil
	})
	if waited {
		if err := <-done; err != nil {
			t.Errorf("the command, after the reading: %v", err)
		}
	}
	if views[0] != views[1] {
		t.Errorf("one reading read two states: %q, then %q", views[0], views[1])
	}
	// The mark is the reading's alone: the next one opens the file the
	// command made.
	if v, err := reading(context.Background(), r, func() (string, error) { return read(), nil }); err != nil || v != "written by a command" {
		t.Errorf("the next reading read %q, %v", v, err)
	}
}

// Where there is no gate (gate_other.go: a system neither Windows nor one with flock),
// a reading and a command each open the state file at once, and neither waits
// for a gate it could never hold, or every reading would wait out its
// patience for one and be refused.
func TestWhereThereIsNoGateNeitherAReadingNorACommandWaitsForOne(t *testing.T) {
	savedPatience, savedGate := lockPatience, gateOf
	lockPatience, gateOf = 3*time.Second, noGate
	t.Cleanup(func() { lockPatience, gateOf = savedPatience, savedGate })
	a, r, _ := twoOpens(t)
	began := time.Now()
	if _, err := reading(context.Background(), r, func() (struct{}, error) { return struct{}{}, nil }); err != nil {
		t.Fatalf("a reading: %v", err)
	}
	if err := a.lockedFinishedDo(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("a command: %v", err)
	}
	if d := time.Since(began); d > time.Second {
		t.Errorf("they took %v, as if waiting for a gate", d)
	}
}

// The goroutines of one Follow, its loop's and its webhook's, are one command:
// one that waits for the slot while another of them holds it tells
// Options.Waiting nothing, and one of another command, or of another Follow,
// tells it once (#927 item 2). Each wait is seen in the slot itself, on no
// clock.
func TestAFollowsOwnGoroutinesWaitForEachOtherUntold(t *testing.T) {
	t.Parallel()
	b, _, _ := twoOpens(t)
	var told atomic.Int32
	b.waiting = func() { told.Add(1) }
	ctx := context.Background()
	one := context.WithValue(ctx, followingKey{}, &following{})
	other := context.WithValue(ctx, followingKey{}, &following{})
	waiting := func() {
		t.Helper()
		for {
			b.slot.mu.Lock()
			w := b.slot.free != nil
			b.slot.mu.Unlock()
			if w {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want int32
	}{
		{"the same Follow", one, 0},
		{"another Follow", other, 1},
		{"no Follow", ctx, 1},
	} {
		told.Store(0)
		release, err := b.claim(one)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			r, err := b.claim(tc.ctx)
			if err == nil {
				r()
			}
			done <- err
		}()
		waiting()
		release()
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if n := told.Load(); n != tc.want {
			t.Errorf("%s: Waiting was told %d times, want %d", tc.name, n, tc.want)
		}
	}
}
