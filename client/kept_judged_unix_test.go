//go:build unix

package client_test

import (
	"context"
	"path/filepath"
	"testing"
)

// Options.UnsafeInboxPermissions lets every directory of the mailbox, and an
// outbox entry, that others can write through: the mailbox receives and sends
// as it would with each its owner's alone. Without it, the same mailbox is
// refused.
func TestTheUnsafeInboxOptionCoversEveryDirectoryAndOutboxEntry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e, a, b := busyMailbox(t)
	for _, rel := range mailboxDirs(t, b) {
		openToWrites(t, filepath.Join(b.Dir(), rel))
	}
	entries, err := filepath.Glob(filepath.Join(b.Dir(), "outbox", "*.P"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("outbox: %q, %v", entries, err)
	}
	openFileToWrites(t, entries[0])
	if _, err := b.Receive(ctx); err == nil {
		t.Fatal("received, with the directories open to others' writes and no option")
	}
	opt := e.heldOpts()
	opt.UnsafeInboxPermissions = true
	u := mustOpen(t, b.Dir(), opt)
	if res, err := u.Receive(ctx); err != nil || len(res.Delivered)+len(res.Held) != 1 {
		t.Fatalf("recv with the option: %+v, %v", res, err)
	}
	before := e.srv.Count(a.Address())
	if sent, err := u.Flush(ctx); err != nil || len(sent) != 1 || e.srv.Count(a.Address()) != before+1 {
		t.Fatalf("flush with the option: %v, %v", sent, err)
	}
}
