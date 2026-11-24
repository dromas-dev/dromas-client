package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/dromas-dev/dromas-client/client"
)

// webhookTries is how many posts --webhook makes for one message at most. The
// tries are at once, then after each of webhookDelays: how Standard Webhooks'
// example schedule and Svix's begin (read 2026-11-17). They stop there, where
// those and Stripe's go on for days, as GitHub's, which tries once: --webhook
// hands a message to an endpoint as a notification, and inbox/ keeps it, so a
// longer failure is its owner's to see (dromas status) and to have posted
// again (dromas inbox retry-failed), as GitHub's redelivery does on request.
const webhookTries = 3

// webhookDelays are the waits before the second and the third post.
var webhookDelays = [webhookTries - 1]time.Duration{5 * time.Second, 5 * time.Minute}

// webhookLongest is the longest wait a Retry-After lengthens one to.
const webhookLongest = 5 * time.Minute

// webhookClient makes --webhook's posts where it is not nil: a test's, and
// otherwise client.NewWebhook's own.
var webhookClient *http.Client

// webhookWait is how long recv waits after the failed post try, whose answer
// asked for retryAfter: the schedule's wait, or the longer Retry-After, up to
// webhookLongest.
func webhookWait(try int, retryAfter time.Duration) time.Duration {
	return max(webhookDelays[try-1], min(retryAfter, webhookLongest))
}

// webhook is FollowOptions.Webhook: it posts each delivery's message line to
// --webhook's URL, up to webhookTries times, and says each failed try. It runs
// on a goroutine of its own, apart from --exec's program.
func (r *recvRun) webhook(ctx context.Context, ev client.Event) error {
	switch ev.Kind {
	case client.EventDelivery:
		// The line --exec's program gets, as recv prints it without --headers.
		return r.post(ctx, ev.Delivery.ID, message(*ev.Delivery, namesOf(ctx, r.b)))
	case client.EventOwedGone:
		r.print(line("webhook_skipped", "id", ev.ID, "detail", "this message was owed its webhook post, and inbox/ no longer keeps it as recv wrote it: nothing was posted"))
	}
	return nil
}

// post posts one message's line, body, and returns client.ErrGaveUp where no
// try is left, and ctx's error where ctx ended a try, whose message stays owed
// for the next start.
func (r *recvRun) post(ctx context.Context, id string, body []byte) error {
	for try := 1; ; try++ {
		err := r.o.webhook.Post(ctx, id, body)
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err == nil {
			return nil
		}
		var we *client.WebhookError
		if !errors.As(err, &we) {
			we = &client.WebhookError{Failure: "no answer"}
		}
		how := []any{"failure", we.Failure}
		if we.Status != 0 {
			how = []any{"status", we.Status}
		}
		r.print(line("webhook_attempt_failed", append(append([]any{"id", id, "attempt", try}, how...),
			"detail", fmt.Sprintf("the webhook post failed at try %d of at most %d", try, webhookTries))...))
		if try == webhookTries {
			r.gaveUp.Store(true)
			r.print(line("webhook_failed", "id", id, "attempts", try, "detail",
				"the webhook has no try left: inbox/ keeps the message, and dromas status lists it until dromas inbox retry-failed has the next recv --webhook post it again, or dromas inbox clear-failed"))
			return client.ErrGaveUp
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(webhookWait(try, we.RetryAfter)):
		}
	}
}
