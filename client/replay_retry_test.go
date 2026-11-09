package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

// Control at the hour's edge: the retry posted at W + TTL is kept by the
// server, the reader prunes with its clock 50 minutes fast at true
// W + 2TTL + 9m59s (an honest server takes it: the reader reads
// W + 2TTL + 59m59s), and the retry is listed once the reader's clock is
// 9m59s slow (it reads W + 2TTL, the retry's last fresh second). The first
// copy's line is still there: SeenFor's hour, to the second. With SeenFor
// two seconds shorter this fails.
func TestARetryAfterAStepOfTheHour(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	off := time.Duration(0)
	r, err := client.Init(ctx, t.TempDir(), e.skewed(&off))
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, r)
	s := e.mailbox()
	mode := "lose"
	var retry []byte
	e.srv.SetMiddle(func(w http.ResponseWriter, q *http.Request, next http.Handler) {
		if q.Method == http.MethodPost && q.URL.Path == "/v1/"+r.Address().String()+"/messages" {
			switch mode {
			case "lose":
				next.ServeHTTP(httptest.NewRecorder(), q)
				http.Error(w, "lost", http.StatusServiceUnavailable)
				return
			case "keep":
				retry, _ = io.ReadAll(q.Body)
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(w, q)
	})
	w := e.clock.Now()
	if _, err := s.Send(ctx, r.Address(), "pay invoice 9 once"); err == nil {
		t.Fatal("the answer came")
	}
	if res := receive(t, r); len(res.Delivered) != 1 {
		t.Fatalf("the first copy: %d", len(res.Delivered))
	}
	mode = ""
	e.clock.Add(protocol.TTL - 2*time.Hour)
	receive(t, r) // renews: the prekey the retry is sealed to
	e.clock.Add(time.Hour)
	receive(t, s)
	e.clock.Add(time.Hour)
	mode = "keep"
	if _, err := s.Flush(ctx); err == nil || retry == nil {
		t.Fatalf("the retry at W+TTL: %v", err)
	}
	mode = ""
	e.clock.Add(w.Add(2*protocol.TTL + 9*time.Minute + 59*time.Second).Sub(e.clock.Now()))
	off = 50 * time.Minute
	if _, err := r.Receive(ctx); err != nil {
		t.Fatalf("the drain 50 minutes fast: %v", err)
	}
	off = -(9*time.Minute + 59*time.Second)
	id := sha256.Sum256(retry)
	e.srv.Inject(r.Address(), hex.EncodeToString(id[:]), retry)
	res, err := r.Receive(ctx)
	t.Logf("the retry, read at W+2TTL by the reader's clock: delivered %d, duplicates %d, refused %v, %v", len(res.Delivered), len(res.Duplicates), res.Refused, err)
	if len(res.Delivered) != 0 || len(res.Duplicates) != 1 {
		t.Error("the retry was not dropped by its msg_id")
	}
}

// The Go client's second seal after 409 stale_prekey is dated when it is
// made, with no second look at the outbox's TTL. The hostile server
// answers the mailbox read and the post each after 59s (under the client's
// one-minute request timeout), the post 409 stale_prekey, and keeps the
// retry sealed again: dated 1m58s past W + TTL. The reader's clock is then
// 50m fast at the drain that prunes and 9m59s slow a second later: passes an
// honest server takes, which the fake server checks as one does.
func TestAStretchedRetryAtClocksAnHonestServerTakes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fast, slow := 50*time.Minute, -(9*time.Minute + 59*time.Second)
	for _, c := range []struct {
		name          string
		stretch       bool          // the mailbox read and the post each answered after 59s, the post 409 stale_prekey
		slowRead      bool          // the mailbox read answered after 59s, the post kept, no 409
		prune         time.Duration // true time of the drain that prunes, after W + 2TTL
		fast, slow    time.Duration // the reader's clock at the drain that prunes, and at the replay
		refuseRenewal bool          // the renewal the pruning drain makes is refused, so the retry's key stays the one in use
		hostile       time.Duration // the server's clock moved with the reader's at the drain that prunes: a pass an honest server refuses
	}{
		{"stretched, 50m fast then 9m59s slow", true, false, 10*time.Minute + time.Second, fast, slow, true, 0},
		{"stretched, one drain 3h ahead taken by the server, then right", true, false, -2*time.Hour + 2*time.Second, 0, 0, true, 3 * time.Hour},
		{"control: stretched, 50m fast then 9m59s slow, the renewal taken", true, false, 10*time.Minute + time.Second, fast, slow, false, 0},
		{"control: not stretched, 50m fast then 9m59s slow", false, false, 10*time.Minute + time.Second, fast, slow, true, 0},
		{"control: a mailbox read of 59s and no 409, 50m fast then 9m59s slow", false, true, 10*time.Minute + time.Second, fast, slow, true, 0},
		{"control: stretched, clocks right, read in the retry's life", true, false, time.Minute, 0, 0, true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			off := time.Duration(0)
			r, err := client.Init(ctx, t.TempDir(), e.skewed(&off))
			if err != nil {
				t.Fatal(err)
			}
			landAll(t, r)
			s := e.mailbox()
			mailbox := "/v1/" + r.Address().String()
			mode := "lose"
			posts, reads := 0, 0
			var retry []byte
			e.srv.SetMiddle(func(w http.ResponseWriter, q *http.Request, next http.Handler) {
				switch {
				case q.Method == http.MethodPost && q.URL.Path == mailbox+"/messages" && mode == "lose":
					next.ServeHTTP(httptest.NewRecorder(), q)
					http.Error(w, "lost", http.StatusServiceUnavailable)
					return
				case q.Method == http.MethodGet && q.URL.Path == mailbox && mode == "keep" && (c.stretch || c.slowRead):
					reads++
					if reads == 1 {
						e.clock.Add(59 * time.Second) // the mailbox read answered after 59s
					}
				case q.Method == http.MethodPost && q.URL.Path == mailbox+"/messages" && mode == "keep":
					posts++
					if c.stretch && posts == 1 {
						e.clock.Add(59 * time.Second) // the post answered after 59s: 409 stale_prekey
						code := protocol.CodeStalePrekey
						w.Header().Set("Content-Type", protocol.ProblemContentType)
						w.WriteHeader(code.Status())
						_ = json.NewEncoder(w).Encode(protocol.Problem{Type: code.TypeURI(), Title: code.Title(), Status: int64(code.Status()), Code: code, Detail: "a newer prekey"})
						return
					}
					retry, _ = io.ReadAll(q.Body) // kept, answered 503
					http.Error(w, "down", http.StatusServiceUnavailable)
					return
				case q.Method == http.MethodPut && mode == "norenew":
					http.Error(w, "down", http.StatusServiceUnavailable)
					return
				}
				next.ServeHTTP(w, q)
			})
			w := e.clock.Now()
			if _, err := s.Send(ctx, r.Address(), "pay invoice 11 once"); err == nil {
				t.Fatal("the first answer came")
			}
			first := receive(t, r)
			if len(first.Delivered) != 1 {
				t.Fatalf("the first copy: %d", len(first.Delivered))
			}
			p1 := first.Delivered[0].Envelope.SentAt
			mode = ""
			e.clock.Add(protocol.TTL - 2*time.Hour)
			receive(t, r) // renews: the prekey the retry is sealed to, which lasts past the drain that prunes
			e.clock.Add(time.Hour)
			receive(t, s)
			e.clock.Add(time.Hour) // W + TTL: the outbox's last second
			mode = "keep"
			_, err = s.Flush(ctx)
			mode = ""
			if retry == nil {
				if c.stretch || c.slowRead {
					t.Logf("no retry was posted: %v", err)
					return
				}
				t.Fatalf("the retry: %v", err)
			}
			e.clock.Add(w.Add(2*protocol.TTL + c.prune).Sub(e.clock.Now()))
			off = c.fast
			if c.refuseRenewal {
				mode = "norenew"
			}
			e.clock.Add(c.hostile)
			if _, err := r.Receive(ctx); err != nil {
				t.Logf("the drain that prunes: %v", err)
			}
			e.clock.Add(-c.hostile)
			mode = ""
			floor, _ := stateOf(r, "seen-floor")
			e.clock.Add(time.Second)
			off = c.slow
			id := sha256.Sum256(retry)
			e.srv.Inject(r.Address(), hex.EncodeToString(id[:]), retry)
			res, err := r.Receive(ctx)
			dated := ""
			if len(res.Delivered) > 0 {
				dated = " (its sent_at the first copy's + " + res.Delivered[0].Envelope.SentAt.Sub(p1).String() + ")"
			}
			t.Logf("floor %q: delivered %d%s, duplicates %d, refused %v, %v", bytes.TrimSpace(floor), len(res.Delivered), dated, len(res.Duplicates), res.Refused, err)
			if len(res.Delivered) != 0 {
				t.Error("a message already delivered was delivered again")
			}
		})
	}
}
