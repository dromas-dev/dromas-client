package client_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// lostState deletes b's state file, leaving its keys, as a lost state file does,
// acknowledges the restore as dromas init --restore does (Mailbox.Acknowledge),
// and gives the mailbox again, with the policy env.mailbox gives.
func lostState(t *testing.T, e *env, b *client.Mailbox) *client.Mailbox {
	t.Helper()
	x := lostUnacknowledged(t, e, b)
	if r, err := x.Acknowledge(t.Context()); err != nil || !r.Acknowledged() {
		t.Fatalf("the acknowledgement of the restore: %+v, %v", r, err)
	}
	return x
}

// lostUnacknowledged deletes b's state file, leaving its keys, and gives the
// mailbox again, its restore not acknowledged.
func lostUnacknowledged(t *testing.T, e *env, b *client.Mailbox) *client.Mailbox {
	t.Helper()
	if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
		t.Fatal(err)
	}
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, x)
	return x
}

// waitedDelivered holds what res's drain did with the messages that waited at a
// restore: each delivered, signed by from, which shows that a key the state
// file adopted decrypted it. A restore keeps no floor: a lost state is a
// failure, and its cost is that a server may deliver again what the mailbox
// accepted before (PROTOCOL.md §15.9).
func waitedDelivered(t *testing.T, res *client.Result, from *client.Mailbox, n int) {
	t.Helper()
	if len(res.Delivered) != n || len(res.Refused) != 0 {
		t.Fatalf("the drain after the restore: %+v, want %d delivered", res, n)
	}
	for _, d := range res.Delivered {
		if d.Envelope.From != from.Address() {
			t.Fatalf("delivered from %s, want %s", d.Envelope.From, from.Address())
		}
	}
}

// pastItsRenewal sets e's clock a minute past the deletion time of p, the
// renewal that retired the keys a restore adopted: its not_after plus the TTL,
// as prune deletes any retired prekey.
func pastItsRenewal(e *env, p *protocol.Prekey) {
	e.clock.Add(p.NotAfter.Add(protocol.TTL + time.Minute).Sub(e.clock.Now()))
}

// refusePrekeyUploads has e's server refuse every renewal, while it answers the rest.
func refusePrekeyUploads(e *env) {
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/prekey") {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// State deleted and keys kept: an adopted key is a retired prekey of the renewal
// above it, and goes by that renewal's deletion time, as prune deletes any
// retired prekey. A renewal after that one does not move the time.
func TestAnAdoptedKeyGoesByTheRenewalAboveIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	old := numberOf(t, b)
	file := prekeyFile(b, old, ".agekey")
	x := lostState(t, e, b)
	if got := client.AdoptedKeys(t, x); !slices.Equal(got, []uint64{old}) {
		t.Fatalf("adopted %v, want %d", got, old)
	}
	e.clock.Add(2 * time.Second)
	p, err := x.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Hour)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(p.NotAfter.Add(protocol.TTL - time.Minute).Sub(e.clock.Now()))
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(file) {
		t.Fatal("a renewal before its time deleted the key")
	}
	pastItsRenewal(e, p)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(file) || len(client.AdoptedKeys(t, x)) != 0 {
		t.Fatalf("a renewal past its time left the key, adopted %v", client.AdoptedKeys(t, x))
	}
}

// A message sealed to the key before the state file is lost, and one sealed to it
// after that and before the renewal, decrypt with the key and are delivered, the
// failure's cost (PROTOCOL.md §15.9). A drain that empties the mailbox more than
// an hour after the server took the renewal deletes the key, as it deletes any
// retired prekey (dropRetired).
func TestAnEmptyingDrainDeletesAnAdoptedKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "sealed to the key the state file names")
	old := numberOf(t, b)
	x := lostState(t, e, b)
	send(t, a, x, "sealed to it with no state file to name it")
	e.clock.Add(2 * time.Second)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatalf("recv after the state file was lost: %v", err)
	}
	waitedDelivered(t, res, a, 2)
	if !exists(prekeyFile(b, old, ".agekey")) {
		t.Fatal("a drain within an hour of the renewal deleted the key")
	}
	e.clock.Add(3 * time.Hour)
	if _, err := x.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(prekeyFile(b, old, ".agekey")) || len(client.AdoptedKeys(t, x)) != 0 {
		t.Fatal("an emptying drain past an hour left the adopted key")
	}
}

// A number by the clock that lands on a file adopted takes no old key for a new
// one: the renewal goes above it, its key is new, and the file stays as it was.
func TestANewNumberPassesAnAdoptedKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	key, err := os.ReadFile(prekeyFile(b, numberOf(t, b), ".agekey"))
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(time.Hour)
	at := uint64(e.clock.Now().Unix()) //nolint:gosec // a test's clock
	if err := os.WriteFile(prekeyFile(b, at, ".agekey"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	x := lostState(t, e, b)
	p, err := x.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Serial <= at {
		t.Fatalf("renewed at %d, at or below the adopted %d", p.Serial, at)
	}
	data, err := os.ReadFile(prekeyFile(b, p.Serial, ".agekey"))
	if err != nil || bytes.Equal(data, key) {
		t.Fatalf("the renewal took the adopted key as its own: %v", err)
	}
	if got := client.AdoptedKeys(t, x); !slices.Contains(got, at) {
		t.Fatalf("adopted %v", got)
	}
}

// The prekey published last is only what a take records: the newest adopted key
// is none, so a mailbox with its state lost reads as having published nothing, and
// its first recv renews as a restore does.
func TestTheNewestAdoptedKeyIsNotThePublishedOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	old := numberOf(t, b)
	x := lostState(t, e, b)
	st, err := x.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.PrekeySerial != 0 {
		t.Fatalf("status names prekey %d", st.PrekeySerial)
	}
	e.clock.Add(2 * time.Second)
	res, err := x.Receive(ctx)
	if err != nil || res.Renewed == nil || res.Renewed.Serial <= old {
		t.Fatalf("the first recv: %+v, %v", res, err)
	}
}

// A state file that records its folders made refuses a prekeys/ gone missing
// before the match of the state and prekeys/ reads it, so a volume not mounted
// marks nothing, and a key it holds that the state file does not record stops
// the next drain once it returns, and is adopted once that is acknowledged.
func TestAFolderMissingAdoptsNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	key, err := os.ReadFile(prekeyFile(b, numberOf(t, b), ".agekey"))
	if err != nil {
		t.Fatal(err)
	}
	orphan := numberOf(t, b) - 1
	if err := os.WriteFile(prekeyFile(b, orphan, ".agekey"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	back := moveAside(t, b.Path("prekeys"))
	if _, err := b.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), b.Path("prekeys")) {
		t.Fatalf("recv with prekeys/ moved aside: %v", err)
	}
	back()
	if _, err := b.Receive(t.Context()); !client.NotTheKeys(err) || len(client.AdoptedKeys(t, b)) != 0 {
		t.Fatalf("recv once prekeys/ is back: %v", err)
	}
	if r, err := b.Acknowledge(t.Context()); err != nil || r.Adopted != 1 {
		t.Fatalf("the acknowledgement: %+v, %v", r, err)
	}
	if got := client.AdoptedKeys(t, b); !slices.Equal(got, []uint64{orphan}) {
		t.Fatalf("adopted %v, want %d", got, orphan)
	}
}

// A file whose name no serial reads is no key this client made: it stays past any
// time.
func TestAKeyFileWithNoSerialStays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	key, err := os.ReadFile(prekeyFile(b, numberOf(t, b), ".agekey"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(prekeyFile(b, 1, ""))
	for _, name := range []string{"mine", "007", "0", strconv.FormatUint(protocol.MaxSerial+1, 10)} {
		if err := os.WriteFile(filepath.Join(dir, name+".agekey"), key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := numberOf(t, b)
	x := lostState(t, e, b)
	if got := client.AdoptedKeys(t, x); !slices.Equal(got, []uint64{old}) {
		t.Fatalf("adopted %v, want %d alone", got, old)
	}
	e.clock.Add(2 * time.Second)
	p, err := x.Rekey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pastItsRenewal(e, p)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(dir, "mine.agekey")) {
		t.Fatal("a file this client did not make was deleted")
	}
}

// A state file adopts once: a key made since, signed or not, is its renewal's, and
// a renewal killed before its statement is signed still uses its number and key.
func TestAKeyMadeSinceIsNotAdopted(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	b := e.mailbox()
	e.clock.Add(2 * time.Second)
	crash(t, e, b, "rekey", "prekey made", protocol.Address{})
	left := begunAt(t, b)
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	p, err := x.Rekey(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if p.Serial != left {
		t.Fatalf("renewed at %d, the number left unsigned is %d", p.Serial, left)
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 {
		t.Fatalf("adopted %v", got)
	}
}

// A rotating-keys-dir not mounted when the state file is lost is made again empty,
// so the restore adopts nothing; once the volume is back, its keys stop the next
// drain before it lists anything, and dromas init --restore adopts them. A copy
// the server kept of a message the lost state delivered, which those keys open,
// is then delivered again: the failure's cost (PROTOCOL.md §15.9).
func TestAKeyOnAVolumeMountedLateStopsUntilAdopted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "delivered before the state file was lost")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the first delivery: %+v", res)
	}
	keys := b.Path("prekeys")
	if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(keys, keys+".unmounted"); err != nil {
		t.Fatal(err)
	}
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, x)
	e.clock.Add(2 * time.Second)
	if res, err := x.Receive(ctx); err != nil || res.Renewed == nil {
		t.Fatalf("the restore with its keys' volume not mounted: %+v, %v", res, err)
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 || has(x, "seen-floor") {
		t.Fatalf("adopted %v with no key there, and holds a floor", got)
	}
	old, err := filepath.Glob(filepath.Join(keys+".unmounted", "*.agekey"))
	if err != nil || len(old) == 0 {
		t.Fatalf("the volume's keys: %v, %v", old, err)
	}
	for _, name := range old {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(keys, filepath.Base(name)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.clock.Add(2 * time.Second)
	id := sha256.Sum256(*sent)
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // the server kept its copy
	var lists atomic.Int32
	watch(e, func(_ http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/messages") {
			lists.Add(1)
		}
		return false
	})
	if _, err := x.Receive(ctx); !client.NotTheKeys(err) || lists.Load() != 0 || e.srv.Count(x.Address()) != 1 {
		t.Fatalf("recv once the volume is back: %v, with %d calls about messages", err, lists.Load())
	}
	e.srv.SetMiddle(nil)
	restoreAcknowledged(t, e, x, 1)
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitedDelivered(t, res, a, 1)
}

// restoreAcknowledged runs dromas init --restore on x, as its owner does, and
// holds what it found: the keys it adopted.
func restoreAcknowledged(t *testing.T, e *env, x *client.Mailbox, adopted int) {
	t.Helper()
	opt := e.opts()
	opt.Restore = true
	b, err := client.Init(t.Context(), x.Dir(), opt)
	if err != nil {
		t.Fatalf("init --restore: %v", err)
	}
	if r := b.Restored(); r == nil || r.Adopted != adopted || !r.Acknowledged() {
		t.Fatalf("init --restore found %+v, want %d adopted", r, adopted)
	}
}

// A restore of a mailbox whose state still records its renewal, with a key in
// prekeys/ the state names nowhere, renews at once: the prekey published is a
// new one, and a message to it is delivered (Options.Restore).
func TestARestoreOfARegisteredMailboxRenewsAtOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	before := numberOf(t, b)
	key, err := os.ReadFile(prekeyFile(b, before, ".agekey"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prekeyFile(b, before-1, ".agekey"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	restoreAcknowledged(t, e, b, 1)
	if n := numberOf(t, b); n == before || slices.Contains(client.AdoptedKeys(t, b), n) {
		t.Fatalf("init --restore left prekey %d published, from %d before it: it renewed nothing", n, before)
	}
	send(t, a, b, "to the new prekey, at once")
	if res := receive(t, b); len(res.Delivered) != 1 || len(res.Refused) != 0 {
		t.Fatalf("a message to the restore's new prekey: %+v, want it delivered", res)
	}
}

// A key file whose name is no serial is never adopted, so it decrypts nothing:
// with the state file lost, the copy the server kept of a message the lost state
// delivered, which only that key opens, is refused as undecryptable, never
// delivered again (PROTOCOL.md §12.3 checks 1 and 8).
func TestAKeyUnderANameNoSerialDecryptsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "delivered before the state file was lost")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the first delivery: %+v", res)
	}
	keys := b.Path("prekeys")
	old, err := filepath.Glob(filepath.Join(keys, "*.agekey"))
	if err != nil || len(old) == 0 {
		t.Fatalf("keys: %v, %v", old, err)
	}
	for _, name := range old {
		if err := os.Rename(name, filepath.Join(keys, "old-"+filepath.Base(name))); err != nil {
			t.Fatal(err)
		}
	}
	x := lostUnacknowledged(t, e, b) // no key under a serial: nothing to acknowledge
	e.clock.Add(2 * time.Second)
	id := sha256.Sum256(*sent)
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // the server kept its copy
	res, err := x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 {
		t.Fatalf("adopted %v", got)
	}
	if len(res.Delivered) != 0 || len(res.Refused) != 1 || res.Refused[0].Code != client.RefusedDecrypt {
		t.Fatalf("the copy the server kept: %+v, want it refused as undecryptable", res)
	}
}

// A key that appears in prekeys/ during a drain, after the command matched the
// state and prekeys/, decrypts nothing in that drain; the next command stops on
// it, and its acknowledgement adopts it (PROTOCOL.md §12.3
// check 8).
func TestAKeyMountedDuringADrainDecryptsNothingUntilAdopted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	sent := forge.Capture(e.srv, http.MethodPost, "/v1/"+b.Address().String()+"/messages")
	send(t, a, b, "delivered before the state file was lost")
	e.srv.SetMiddle(nil)
	if res := receive(t, b); len(res.Delivered) != 1 {
		t.Fatalf("the first delivery: %+v", res)
	}
	keys := b.Path("prekeys")
	if err := os.Remove(filepath.Join(b.Dir(), "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(keys, keys+".unmounted"); err != nil {
		t.Fatal(err)
	}
	x, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	landAll(t, x)
	e.clock.Add(2 * time.Second)
	if res, err := x.Receive(ctx); err != nil || res.Renewed == nil {
		t.Fatalf("the restore with its keys' volume not mounted: %+v, %v", res, err)
	}
	old, err := filepath.Glob(filepath.Join(keys+".unmounted", "*.agekey"))
	if err != nil || len(old) == 0 {
		t.Fatalf("the volume's keys: %v, %v", old, err)
	}
	e.clock.Add(2 * time.Second)
	id := sha256.Sum256(*sent)
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // the server kept its copy
	var mounted atomic.Bool
	e.srv.SetMiddle(func(w http.ResponseWriter, r *http.Request, next http.Handler) {
		// The volume comes back as the drain's list is answered.
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages") && mounted.CompareAndSwap(false, true) {
			for _, name := range old {
				data, err := os.ReadFile(name)
				if err == nil {
					err = os.WriteFile(filepath.Join(keys, filepath.Base(name)), data, 0o600)
				}
				if err != nil {
					t.Error(err)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
	res, err := x.Receive(ctx)
	e.srv.SetMiddle(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !mounted.Load() || len(res.Delivered) != 0 || len(res.Refused) != 1 || res.Refused[0].Code != client.RefusedDecrypt {
		t.Fatalf("the drain the volume came back in: %+v, want the copy refused as undecryptable", res)
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 {
		t.Fatalf("adopted %v in the drain the volume came back in", got)
	}
	e.srv.Inject(x.Address(), hex.EncodeToString(id[:]), *sent) // and serves it again
	e.clock.Add(2 * time.Second)
	if _, err := x.Receive(ctx); !client.NotTheKeys(err) || e.srv.Count(x.Address()) != 1 {
		t.Fatalf("the next command: %v", err)
	}
	if r, err := x.Acknowledge(ctx); err != nil || r.Adopted == 0 {
		t.Fatalf("the acknowledgement adopted none of the volume's keys: %+v, %v", r, err)
	}
	res, err = x.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitedDelivered(t, res, a, 1)
}
