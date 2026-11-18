package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// bodies is a handler that keeps the body of each delivery it is handed, in
// order, and returns what result says for it.
type bodies struct {
	mu     sync.Mutex
	got    []string
	result func(body string) error
}

func (h *bodies) handle(_ context.Context, ev client.Event) error {
	if ev.Kind != client.EventDelivery {
		return nil
	}
	var body string
	if err := json.Unmarshal(ev.Delivery.Envelope.Body, &body); err != nil {
		return err
	}
	h.mu.Lock()
	h.got = append(h.got, body)
	h.mu.Unlock()
	if h.result == nil {
		return nil
	}
	return h.result(body)
}

func (h *bodies) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.got)
}

func gaveUp(string) error { return client.ErrGaveUp }

// The webhook is a pipeline apart from the handler, in each direction: where
// one gives up on every message and the other takes each, each is handed every
// message in delivery order, the one that gave up has a record of each, the
// other none, and nothing is owed to either.
func TestTheWebhookAndTheHandlerFailApart(t *testing.T) {
	t.Parallel()
	for _, failing := range []string{"handler", "webhook"} {
		t.Run(failing, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			a, b := e.mailbox(), e.mailbox()
			want := []string{"one", "two", "three"}
			for _, body := range want {
				send(t, a, b, body)
			}
			handler, hook := &bodies{}, &bodies{}
			if failing == "handler" {
				handler.result = gaveUp
			} else {
				hook.result = gaveUp
			}
			if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Owed: true, Webhook: hook.handle}, handler.handle); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(handler.list(), want) || !slices.Equal(hook.list(), want) {
				t.Fatalf("the handler was handed %q and the webhook %q, want %q each", handler.list(), hook.list(), want)
			}
			st, err := b.Status(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			failed, other := st.GaveUp, st.WebhookGaveUp
			if failing == "webhook" {
				failed, other = other, failed
			}
			if len(failed) != 3 || len(other) != 0 || st.Owed != 0 || st.WebhookOwed != 0 {
				t.Fatalf("status: %+v", st)
			}
			if !slices.ContainsFunc(st.Attention, func(s string) bool {
				return strings.Contains(s, failing) || failing == "handler" && strings.Contains(s, "callback")
			}) {
				t.Fatalf("attention names no %s: %q", failing, st.Attention)
			}
		})
	}
}

// A webhook that waits holds back nothing of the handler's: the handler is
// handed every message of the drain while the webhook has the first, and the
// webhook is handed the rest after, in order, before Follow returns.
func TestASlowWebhookDelaysNothingOfTheHandler(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	want := []string{"one", "two", "three"}
	for _, body := range want {
		send(t, a, b, body)
	}
	all := make(chan struct{})
	handler := &bodies{}
	handler.result = func(string) error {
		if len(handler.list()) == len(want) {
			close(all)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	hook := &bodies{result: func(string) error {
		select {
		case <-all:
			return nil
		case <-ctx.Done():
			return errors.New("the handler never had every message while the webhook waited")
		}
	}}
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Owed: true, Webhook: hook.handle}, handler.handle); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hook.list(), want) {
		t.Fatalf("the webhook was handed %q, want %q", hook.list(), want)
	}
}

// What the webhook leaves owed, by an error that is not ErrGaveUp, is counted,
// and the next Follow hands it to the webhook first, in order, and not to the
// handler again.
func TestWhatTheWebhookLeavesOwedIsHandedOverAtTheNextStart(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "one")
	send(t, a, b, "two")
	handler := &bodies{}
	cut := &bodies{result: func(string) error { return errors.New("cut short") }}
	if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Owed: true, Webhook: cut.handle}, handler.handle); err != nil {
		t.Fatal(err)
	}
	if st, err := b.Status(context.Background()); err != nil || st.WebhookOwed != 2 || st.Owed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
	hook := &bodies{}
	if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Owed: true, Webhook: hook.handle}, handler.handle); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hook.list(), []string{"one", "two"}) || len(handler.list()) != 2 {
		t.Fatalf("the webhook was handed %q, the handler %q", hook.list(), handler.list())
	}
	if st, err := b.Status(context.Background()); err != nil || st.WebhookOwed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// A message owed the webhook that inbox/ no longer keeps is an EventOwedGone
// to the webhook, whose record then goes.
func TestAMessageOwedTheWebhookAndGoneFromInboxIsSaidOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "deleted")
	cut := &bodies{result: func(string) error { return errors.New("cut short") }}
	if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Webhook: cut.handle}, nil); err != nil {
		t.Fatal(err)
	}
	ids, err := filepath.Glob(filepath.Join(b.Dir(), "mail", "inbox", "*.json"))
	if err != nil || len(ids) != 1 {
		t.Fatalf("inbox/: %v, %v", ids, err)
	}
	if err := os.Remove(ids[0]); err != nil {
		t.Fatal(err)
	}
	ids[0] = strings.TrimSuffix(filepath.Base(ids[0]), ".json")
	var gone []string
	hook := func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventOwedGone {
			gone = append(gone, ev.ID)
		}
		return nil
	}
	for range 2 {
		if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Webhook: hook}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(gone, ids) {
		t.Fatalf("gone: %v, want %v once", gone, ids)
	}
	if st, err := b.Status(context.Background()); err != nil || st.WebhookOwed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// ClearFailed clears the webhook's records beside the handler's, and counts
// both.
func TestClearFailedClearsTheWebhooksRecordsToo(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "one")
	handler, hook := &bodies{result: gaveUp}, &bodies{result: gaveUp}
	if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Owed: true, Webhook: hook.handle}, handler.handle); err != nil {
		t.Fatal(err)
	}
	if st, err := b.Status(context.Background()); err != nil || len(st.GaveUp) != 1 || len(st.WebhookGaveUp) != 1 {
		t.Fatalf("status: %+v, %v", st, err)
	}
	if n, err := b.ClearFailed(context.Background()); err != nil || n != 2 {
		t.Fatalf("ClearFailed: %d, %v, want 2", n, err)
	}
	if st, err := b.Status(context.Background()); err != nil || len(st.GaveUp) != 0 || len(st.WebhookGaveUp) != 0 || len(st.Attention) != 0 {
		t.Fatalf("status after ClearFailed: %+v, %v", st, err)
	}
}

// A message held in held/ is owed nothing: the webhook is handed none.
func TestAHeldMessageIsOwedTheWebhookNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.heldMailbox()
	send(t, a, b, "from a stranger")
	hook := &bodies{}
	if err := b.Follow(context.Background(), client.FollowOptions{Until: time.Now(), Webhook: hook.handle}, nil); err != nil {
		t.Fatal(err)
	}
	if len(hook.list()) != 0 {
		t.Fatalf("the webhook was handed %q", hook.list())
	}
	if st, err := b.Status(context.Background()); err != nil || st.Held != 1 || st.WebhookOwed != 0 {
		t.Fatalf("status: %+v, %v", st, err)
	}
}

// A webhook's error other than ErrGaveUp leaves its message owed and ends the
// handover there: the messages after it stay owed behind it, and the next
// handover begins with it, so the webhook is handed them in delivery order
// (#927 item 4).
func TestAWebhooksErrorKeepsTheDeliveryOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	for _, body := range []string{"one", "two", "three"} {
		send(t, a, b, body)
	}
	failed := false
	hook := &bodies{result: func(body string) error {
		if body == "one" && !failed {
			failed = true
			return errors.New("not now")
		}
		return nil
	}}
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Webhook: hook.handle}, nil); err != nil {
		t.Fatal(err)
	}
	// The first Follow hands "one" alone, or, where a kick came during that
	// handover, hands the three again after it: either is the order.
	want := []string{"one", "one", "two", "three"}
	if got := hook.list(); !slices.Equal(got, want[:1]) && !slices.Equal(got, want) {
		t.Fatalf("after the first Follow: handed %q", got)
	}
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Webhook: hook.handle}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hook.list(), want) {
		t.Errorf("the webhook was handed %q, want %q", hook.list(), want)
	}
	if st, err := b.Status(ctx); err != nil || st.WebhookOwed != 0 {
		t.Errorf("after: %+v, %v", st, err)
	}
}

// A delivery whose kick came while the webhook's handover was under way, and
// Follow's loop ended before that handover did, is handed to the webhook
// before Follow returns (webhooks.run's end) (#927 item 9). The order is
// made, never timed: the handler's owed message, handed over before the
// drain, waits until the webhook has begun the message owed it, which waits
// until Follow's loop has ended (SetFollowDone).
func TestAKickDuringTheLastHandoverIsHandedOverBeforeFollowReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "old")
	failing := func(context.Context, client.Event) error { return errors.New("not yet") }
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Owed: true, Webhook: failing}, failing); err != nil && err.Error() != "not yet" {
		t.Fatal(err)
	}
	send(t, a, b, "new")
	begun, ended := make(chan struct{}), make(chan struct{})
	client.SetFollowDone(b, func() { close(ended) })
	hook := &bodies{result: func(body string) error {
		if body == "old" {
			close(begun)
			<-ended
		}
		return nil
	}}
	handler := func(_ context.Context, ev client.Event) error {
		if ev.Kind == client.EventDelivery && ev.Result == nil { // the owed "old", before the drain
			<-begun
		}
		return nil
	}
	if err := b.Follow(ctx, client.FollowOptions{Until: time.Now(), Owed: true, Webhook: hook.handle}, handler); err != nil {
		t.Fatal(err)
	}
	if st, err := b.Status(ctx); err != nil || st.WebhookOwed != 0 || !slices.Equal(hook.list(), []string{"old", "new"}) {
		t.Errorf("the webhook was handed %q; %+v, %v", hook.list(), st, err)
	}
}

// A webhook made with the caller's http.Client follows no redirect either: the
// redirect's status is the try's answer (#927 item 10).
func TestAWebhookOfTheCallersClientFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	var landed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			landed.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer srv.Close()
	w, err := client.NewWebhook(srv.URL+"/hook", nil, nil, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	err = w.Post(context.Background(), "id", []byte(`{}`))
	var we *client.WebhookError
	if !errors.As(err, &we) || we.Status != http.StatusFound || landed.Load() {
		t.Errorf("Post: %v, the redirect followed %v", err, landed.Load())
	}
}
