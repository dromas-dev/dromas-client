package client_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// lookups records, for each read of a mailbox or of an address's revocations,
// whether it carried a pass.
func lookups(e *env) (seen func() map[string]bool) {
	var mu sync.Mutex
	got := map[string]bool{}
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		_, read := forge.ReadOf(r)
		if _, revocations := forge.RevocationsOf(r); read || revocations {
			mu.Lock()
			got[r.URL.Path] = r.Header.Get("Authorization") != ""
			mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
	return func() map[string]bool {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]bool{}
		for k, v := range got {
			out[k] = v
		}
		return out
	}
}

// A mailbox's reads carry no pass (PROTOCOL.md §11.8, §11.15, §11.5): the
// reads are public, and a server reads none on them. Not the recipient's
// mailbox before a send, not a sender's revocations in a drain, and not a
// mailbox's read of its own.
func TestLookupsCarryNoPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	seen := lookups(e)
	send(t, a, b, "asked about with no pass")
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("delivered %d", len(res.Delivered))
	}
	got := seen()
	for _, p := range []string{mailboxPath(b), mailboxPath(a) + "/revocations"} {
		if carried, asked := got[p]; !asked || carried {
			t.Errorf("GET %s: asked %v, with a pass %v", p, asked, carried)
		}
	}
	seen = lookups(e)
	if _, err := b.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if carried, asked := seen()[mailboxPath(b)]; !asked || carried {
		t.Errorf("a mailbox's read of its own mailbox: asked %v, with a pass %v", asked, carried)
	}
}

// A server that holds an address revoked answers 410 to whatever is sent for
// it, with no check. A revocation published there again is taken as done
// once the record it then asks for verifies, and is not otherwise.
func TestARevocationPublishedToARevokedMailbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a := e.mailbox()
	rec := revoke(t, e, a)
	if _, err := client.Revoke(ctx, e.opts(), rec); err != nil {
		t.Fatalf("published again to a server that holds it: %v", err)
	}
	says(e, protocol.CodeMailboxRevoked, http.MethodPut, "/revocations")
	b := e.mailbox()
	other, err := b.Revocation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Revoke(ctx, e.opts(), other); err == nil {
		t.Fatal("a 410 from a server that holds no revocation was taken as done")
	}
}
