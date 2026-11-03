package client_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/protocol"
)

// lostState deletes b's state file, leaving its keys, as a lost state file does,
// and gives the mailbox again, with the policy env.mailbox gives.
func lostState(t *testing.T, e *env, b *client.Mailbox) *client.Mailbox {
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

// adoptedEnd is an adopted key's deletion time, by the confirmed time of the first
// renewal after its adoption, as the design states it.
func adoptedEnd(took time.Time) time.Time {
	return took.Add(protocol.TTL + time.Hour)
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

// State deleted and keys kept: the old key stays until a reading a server confirmed
// reaches the first renewal's confirmed time plus the TTL and the hour. Later
// renewals do not move that time, and the first such reading past it deletes the
// key.
func TestAnAdoptedKeyGoesByAConfirmedReadingPastItsRenewal(t *testing.T) {
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
	took := e.clock.Now()
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(file) {
		t.Fatal("the renewal after the state file was lost deleted the key")
	}
	e.clock.Add(adoptedEnd(took).Add(-time.Minute).Sub(e.clock.Now()))
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(file) {
		t.Fatal("a reading a minute before its time deleted the key")
	}
	e.clock.Add(2 * time.Minute)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(file) {
		t.Fatal("a reading past its time left the key")
	}
	if got := client.AdoptedKeys(t, x); len(got) != 0 {
		t.Fatalf("still adopted: %v", got)
	}
}

// A drain's list is a confirmed reading as a renewal is, and deletes the key too.
func TestADrainsListDeletesAnAdoptedKeyPastItsTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	old := numberOf(t, b)
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	took := e.clock.Now()
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(adoptedEnd(took).Add(time.Minute).Sub(e.clock.Now()))
	refusePrekeyUploads(e) // the list alone is left to confirm a reading
	if res, err := x.Receive(ctx); err != nil || res.RenewErr == nil {
		t.Fatalf("recv: %+v, %v", res, err)
	}
	if exists(prekeyFile(b, old, ".agekey")) {
		t.Fatal("a drain past its time left the key")
	}
}

// A message sealed to the key before the state file is lost, and one sealed to it
// after that and before the renewal, are delivered, and an emptying drain leaves
// the key: dropRetired reads statements alone.
func TestAMessageSealedToAnAdoptedKeyIsDelivered(t *testing.T) {
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
	if err != nil || len(res.Delivered) != 2 || len(res.Refused) != 0 {
		t.Fatalf("recv after the state file was lost: %+v, %v", res, err)
	}
	e.clock.Add(2 * time.Hour)
	if _, err := x.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(prekeyFile(b, old, ".agekey")) {
		t.Fatal("an emptying drain deleted an adopted key")
	}
}

// A renewal never confirmed deletes nothing, whatever the readings after it, and
// across a further lost state file: the first renewal confirmed then starts the time.
func TestARenewalNeverConfirmedDeletesNoAdoptedKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	old := numberOf(t, b)
	file := prekeyFile(b, old, ".agekey")
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	// The server takes this one, and the mailbox never has the answer.
	crash(t, e, x, "rekey", "renewal taken", protocol.Address{})
	y, err := client.Open(b.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Add(protocol.TTL + 2*time.Hour)
	refusePrekeyUploads(e)
	// Its read records the take with no reading, and its list is a confirmed
	// reading well past any time that take could have given.
	res, err := y.Receive(ctx)
	if err != nil || res.RenewErr == nil || numberOf(t, y) == 0 {
		t.Fatalf("recv with every renewal refused: %+v, %v", res, err)
	}
	if !exists(file) {
		t.Fatal("a reading deleted the key, and no renewal is confirmed")
	}
	z := lostState(t, e, b)
	e.srv.SetMiddle(nil)
	if got := client.AdoptedKeys(t, z); !slices.Contains(got, old) {
		t.Fatalf("the next state file adopted %v", got)
	}
	e.clock.Add(2 * time.Second)
	took := e.clock.Now()
	if _, err := z.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if !exists(file) {
		t.Fatal("the first renewal confirmed deleted the key")
	}
	e.clock.Add(adoptedEnd(took).Add(time.Minute).Sub(e.clock.Now()))
	if _, err := z.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if exists(file) {
		t.Fatal("a reading past that renewal's time left the key")
	}
}

// A clock that jumps past the time after the server took the renewal deletes no
// adopted key, and the renewal is timed by the reading the server took: the message
// that waits is delivered once the clock is right.
func TestAClockThatJumpsDuringARenewalDeletesNoAdoptedKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "waits")
	old := numberOf(t, b)
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	var off time.Duration
	o := e.skewed(&off)
	o.HTTPClient = &http.Client{Transport: jumpAtRenewal{&off, 70 * 24 * time.Hour}}
	jumpy, err := client.Open(x.Dir(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := jumpy.Rekey(ctx); err != nil {
		t.Logf("the renewal: %v", err)
	}
	if !exists(prekeyFile(b, old, ".agekey")) {
		t.Fatal("a clock that jumped after the take deleted the key")
	}
	right, err := client.Open(x.Dir(), e.opts())
	if err != nil {
		t.Fatal(err)
	}
	if res, err := right.Receive(ctx); err != nil || len(res.Delivered) != 1 {
		t.Fatalf("once the clock is right: %+v, %v", res, err)
	}
}

// A clock past the time that no server takes deletes nothing, and the message that
// waits is delivered once the clock is right.
func TestAnUnconfirmedClockDeletesNoAdoptedKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	a, b := e.mailbox(), e.mailbox()
	send(t, a, b, "waits")
	old := numberOf(t, b)
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	off := 70 * 24 * time.Hour
	fast, err := client.Open(b.Dir(), e.skewed(&off))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fast.Receive(ctx); err == nil {
		t.Fatal("a drain at a clock 70 days ahead went through")
	}
	if !exists(prekeyFile(b, old, ".agekey")) {
		t.Fatal("a clock no server took deleted the key")
	}
	off = 0
	if res, err := fast.Receive(ctx); err != nil || len(res.Delivered) != 1 {
		t.Fatalf("once the clock is right: %+v, %v", res, err)
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

// Status counts an adopted key past its time, which no server has confirmed.
func TestStatusCountsAnAdoptedKeyPastItsTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	b := e.mailbox()
	x := lostState(t, e, b)
	e.clock.Add(2 * time.Second)
	took := e.clock.Now()
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(adoptedEnd(took).Add(time.Minute).Sub(e.clock.Now()))
	st, err := x.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(st.Attention, func(s string) bool { return strings.Contains(s, ": 1.") }) {
		t.Fatalf("status: %q", st.Attention)
	}
}

// A state file that records its folders made and has not yet read prekeys/ refuses
// a prekeys/ gone missing before it reads anything, so a volume not mounted marks
// nothing, and the keys are adopted once it returns.
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
	client.ForgetAdoption(t, b)
	back := moveAside(t, b.Path("prekeys"))
	if _, err := b.Receive(t.Context()); err == nil || !strings.Contains(err.Error(), b.Path("prekeys")) {
		t.Fatalf("recv with prekeys/ moved aside: %v", err)
	}
	back()
	receive(t, b)
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
	took := e.clock.Now()
	if _, err := x.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(adoptedEnd(took).Add(time.Minute).Sub(e.clock.Now()))
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
