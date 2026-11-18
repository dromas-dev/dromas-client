//go:build unix

package client_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// On unix, where a file's mode is what judgeKept judges: on Windows a mode is
// not an access list, and a kept file's refusal there is judgeKept's own test.

// Follow returns, where it would return nil, the fault that cut the
// webhook's last handover short: here the refusal of an owed message's file
// that others can write (judgeKept) (#927 item 7).
func TestFollowReturnsTheFaultOfTheWebhooksLastHandover(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "one")
	cut := &bodies{result: func(string) error { return errors.New("cut short") }}
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Webhook: cut.handle}, nil); err != nil {
		t.Fatal(err)
	}
	ids, err := filepath.Glob(filepath.Join(b.Dir(), "mail", "inbox", "*.json"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("inbox/: %v %v", ids, err)
	}
	if err := os.Chmod(ids[0], 0o666); err != nil {
		t.Fatal(err)
	}
	hook := &bodies{}
	err = b.Follow(ctx, client.FollowOptions{Until: time.Now(), Webhook: hook.handle}, nil)
	if err == nil || len(hook.list()) != 0 {
		t.Errorf("Follow: %v, having handed the webhook %q", err, hook.list())
	}
}
