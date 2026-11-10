package sh_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// The clock: every check is exact at the clock of whoever makes it, and what
// a mailbox makes leaves an hour of room. The server keeps the machine's clock;
// a mailbox's is dr_now.

// at is a mailbox on a clock off from the machine's.
type at struct {
	*shMailbox
	off time.Duration
}

func (b at) sh(cmd string) ran {
	b.w.t.Helper()
	return b.shMailbox.sh(igNow(b.off) + cmd)
}

func (b at) must(cmd string) string {
	b.w.t.Helper()
	return b.shMailbox.must(igNow(b.off) + cmd)
}

func (b at) recv() drain {
	b.w.t.Helper()
	return b.shMailbox.recv(igNow(b.off))
}

func (b at) now() int64 { return time.Now().Add(b.off).Unix() }

// A mailbox whose clock is 9 minutes slow, or 49 fast, registers, renews, and
// exchanges messages with a mailbox made that moment. One 11 minutes slow is
// refused at its pass and told what its clock reads beside the server's
// word.
func TestScriptAtTheEdgesOfAClock(t *testing.T) {
	w := newWorld(t)
	for _, off := range []time.Duration{-9 * time.Minute, 49 * time.Minute} {
		s := at{w.empty(), off}
		s.must("dr_init")
		s.addr = mustAddress(t, s.dir)
		f := w.mailbox()
		f.mustSend(s.shMailbox, "to the mailbox whose clock is off")
		if d := s.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"to the mailbox whose clock is off"}) {
			t.Errorf("%v: it reads: %v", off, d)
		}
		if r := s.sh("printf 'from the mailbox whose clock is off' | dr_send " + f.addr.String()); r.rc != 0 {
			t.Errorf("%v: it sends: %v", off, r)
		}
		if r := s.sh("dr_rekey"); r.rc != 0 {
			t.Errorf("%v: it renews: %v", off, r)
		}
		if d := f.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"from the mailbox whose clock is off"}) {
			t.Errorf("%v: its messages are read: %v", off, d)
		}
	}
	slow := at{w.empty(), -11 * time.Minute}
	r := slow.sh("dr_init")
	if r.rc == 0 || !strings.Contains(r.err, "pass_expired") || !strings.Contains(r.err, "the server dates its answer 20") || strings.Contains(r.err, "has run out") ||
		!regexp.MustCompile(`this machine's clock reads .*Check this machine's clock against a time source you trust, not against the server`).MatchString(r.err) || strings.Contains(strings.ToLower(r.err), "set this machine's clock") {
		t.Errorf("a mailbox 11 minutes slow: %v", r)
	}
}

// A mailbox first made, or renewed, while its clock read over an hour ahead, or
// 55 minutes: refused, its statement for ending too far ahead or its pass, and
// with the clock right it registers again with its statement made again, for
// the same prekey, and a renewal it left is spent; a serial never goes back.
func TestScriptAfterAClockAhead(t *testing.T) {
	w := newWorld(t)
	for _, c := range []struct {
		ahead time.Duration
		code  string
	}{{61 * time.Minute, "invalid_validity_period"}, {55 * time.Minute, "pass_expired"}} { // a statement ending more than the longest prekey lifetime and an hour after the server's clock, which it reads before the pass; then a pass ending more than an hour after it
		b := at{w.empty(), c.ahead}
		if r := b.sh("dr_init"); r.rc == 0 || !strings.Contains(r.err, c.code) {
			t.Fatalf("a mailbox %v fast registered: %v", c.ahead, r)
		}
		f1 := b.newest()
		s1, a1 := b.read(fmt.Sprintf("prekeys/%d.txt", f1)), b.read(fmt.Sprintf("prekeys/%d.agekey", f1))
		right := at{b.shMailbox, 0}
		right.must("dr_init")
		b.addr = mustAddress(t, b.dir)
		if b.read(fmt.Sprintf("prekeys/%d.txt", f1)) == s1 || b.read(fmt.Sprintf("prekeys/%d.agekey", f1)) != a1 || b.num() != f1 {
			t.Errorf("%v: with the clock right, not a new first statement for the same prekey at %d: serial %d", c.ahead, f1, b.num())
		}
		ahead := at{b.shMailbox, c.ahead}
		if c.ahead > time.Hour {
			ahead.off = 2 * time.Hour
		}
		s0, t0 := b.num(), ahead.now()
		if r := ahead.sh("dr_rekey"); r.rc == 0 {
			t.Fatalf("a renewal signed %v ahead was taken", ahead.off)
		}
		m := b.newest()
		if m <= s0 || c.ahead > time.Hour && int64(m) < t0 {
			t.Errorf("%v: the renewal left no statement numbered by that clock: %d after %d, at %d", ahead.off, m, s0, t0)
		}
		right.must("dr_rekey")
		if p, _ := w.served(b.shMailbox); b.num() != m+1 || p != m+1 {
			t.Errorf("%v: the renewal with the clock right is not one above the spent one, %d: %d, served %d", ahead.off, m, b.num(), p)
		}
	}
}

// A mailbox's number is its machine's clock once the clock has passed the last
// number, and one above the last when it has not.
func TestScriptNumbersByTheClock(t *testing.T) {
	w := newWorld(t)
	b := w.mailbox()
	n1 := b.num()
	fast := at{b, 90 * time.Second}
	fast.must("dr_rekey")
	n2 := b.num()
	if _, c := w.served(b); int64(n2) > fast.now() || int64(n2) < fast.now()-30 || n2 <= n1+60 || c != 0 {
		t.Errorf("a renewal 90 seconds on: %d after %d, served by a signer of rank %d", n2, n1, c)
	}
	at{b, -90 * time.Second}.must("dr_rekey")
	if p, c := w.served(b); b.num() != n2+1 || p != n2+1 || c != 0 {
		t.Errorf("a renewal with the clock behind its last number: %d after %d, served %d by a signer of rank %d", b.num(), n2, p, c)
	}
}

// A send from a clock two hours fast is refused at its pass and keeps
// nothing; a kept pass is not used once the clock has gone back; a message dated
// more than an hour ahead of its reader is refused as future.
func TestScriptSendsAndReadsByItsClock(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	if r := (at{p, 2 * time.Hour}).sh("printf 'dated two hours ahead' | dr_send " + q.addr.String()); r.rc == 0 || !strings.Contains(r.err, "pass_expired") || !p.bare() {
		t.Errorf("a send from a clock two hours fast: %v", r)
	}
	if d := q.recv(); len(d.messages) != 0 || strings.Contains(d.said, "message_not_yet_valid") || w.srv.Count(q.addr) != 0 {
		t.Errorf("its reader was sent something: %v", d)
	}
	p.must("dr_pass > /dev/null")
	if kept := p.read("pass"); (at{p, -30 * time.Minute}).must("dr_pass") == kept {
		t.Error("the kept pass was used after the clock went back")
	}
	l := p.line(q.addr, "00000000-0000-4000-8000-0000000000f1", "dated ahead", -3700*time.Second)
	w.inject(q, "", l, p.sign(q.addr, l))
	if d := q.recv(); len(d.messages) != 0 || !strings.Contains(d.said, "message_not_yet_valid: sent_at is more than 60 minutes ahead") {
		t.Errorf("sent_at more than an hour ahead of the reader's clock: %v", d)
	}
}

// A kept pass goes only to the server it was signed for (PROTOCOL.md §8): the
// same mailbox calling another server signs a new pass for it.
func TestScriptKeepsItsPassForItsServer(t *testing.T) {
	w := newWorld(t)
	p := w.mailbox()
	kept := p.must("dr_pass")
	if p.must("dr_pass") != kept {
		t.Fatal("control: the kept pass was not used again at its own server")
	}
	if p.must("DR_SERVER_URL=http://127.0.0.1:9 dr_pass") == kept {
		t.Error("the pass kept for one server was used for another")
	}
}

// The replay set: pruned only once the server has answered the list, by the
// mailbox's clock read then, a line kept twice the TTL and an hour from its
// sent_at (protocol.SeenFor), and no floor.
func TestScriptKeepsItsReplaySetByItsClock(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	l := p.line(q.addr, "00000000-0000-4000-8000-0000000000f2", "pay invoice 7 once", protocol.TTL-49*time.Minute)
	sig := p.sign(q.addr, l)
	oid := w.inject(q, "", l, sig)
	if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 7 once"}) {
		t.Fatalf("the first delivery: %v", d)
	}
	if d := (at{q, 50 * time.Minute}).recv(); d.rc != 0 { // the TTL and a minute old by this clock, which a server takes
		t.Fatalf("a drain 50 minutes fast: %v", d)
	}
	w.inject(q, oid, l, sig)
	if d := (at{q, -9 * time.Minute}).recv(); len(d.messages) != 0 || d.rc != 0 || !strings.Contains(q.read("seen"), oid+" ") || q.has("seen-floor") {
		t.Errorf("the server's replay of it after the clock stepped back: %v", d)
	}
	seen := q.read("seen")
	if d := (at{q, 70 * 24 * time.Hour}).recv(); d.rc == 0 || q.read("seen") != seen || q.has("seen-floor") {
		t.Errorf("a recv refused for a clock 70 days fast pruned: %v", d)
	}
	old := fmt.Sprintf("%064d", 7)
	q.must(fmt.Sprintf(`printf '%%s %%s %%s %%s\n' %s %d %s 00000000-0000-4000-8000-0000000000f3 >> "$DR/seen"`, old, time.Now().Add(-protocol.SeenFor-24*time.Hour).Unix(), p.addr))
	q.recv()
	if strings.Contains(q.read("seen"), old+" ") || !strings.Contains(q.read("seen"), oid+" ") || q.has("seen-floor") {
		t.Errorf("the replay set after a prune: %q", q.read("seen"))
	}
}

// A mailbox that loses seen and seen-keys, its prekeys kept, holds keys its
// replay set does not cover: its next drain stops with exit 4 before it lists
// anything, as the Go client's does, until init --restore names them and
// renews. The failure's cost is accepted: a copy the server kept of a message
// it delivered is delivered again, and a message to the new prekey is
// delivered (PROTOCOL.md §15.9).
func TestScriptStopsAfterItsStateIsLostAndAcceptsItsCost(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	l := p.line(q.addr, "00000000-0000-4000-8000-0000000000f6", "pay invoice 8 once", 0)
	ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
	w.put(q, "", ct)
	if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 8 once"}) || q.has("seen-floor") {
		t.Fatalf("the first delivery: %v", d)
	}
	q.must(`rm "$DR/seen" "$DR/seen-keys"`)
	w.put(q, "", ct) // the server kept its copy
	if d := q.recv(); d.rc != 4 || len(d.messages) != 0 || !strings.Contains(d.said, "restore_required") || w.srv.Count(q.addr) != 1 || q.has("seen-floor") {
		t.Fatalf("the drain before init --restore: %v", d)
	}
	serial := q.read("serial")
	if out := q.must("dr_init --restore"); !strings.Contains(out, `"type":"restore","acknowledged":true`) || q.read("serial") == serial {
		t.Fatalf("init --restore: %q, serial %q from %q: it renewed nothing", out, q.read("serial"), serial)
	}
	if d := q.recv(); !slices.Equal(d.bodies(), []string{"pay invoice 8 once"}) || q.has("seen-floor") {
		t.Fatalf("the server's copy after the state was lost: %v, want it delivered again", d)
	}
	now := p.line(q.addr, "00000000-0000-4000-8000-0000000002fa", "to the new prekey", 0)
	w.inject(q, "", now, p.sign(q.addr, now))
	if d := q.recv(); !slices.Equal(d.bodies(), []string{"to the new prekey"}) {
		t.Fatalf("a message to the restore's new prekey: %v", d)
	}
}

// A key file whose name is no serial is never named in seen-keys, so it decrypts
// nothing: after seen and seen-keys are lost, the copy the server kept of a
// message the lost set delivered, which only that key opens, is not delivered
// again, as in the Go client (TestAKeyUnderANameNoSerialDecryptsNothing).
func TestScriptAKeyUnderANameNoSerialDecryptsNothing(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	l := p.line(q.addr, "00000000-0000-4000-8000-0000000000f9", "pay invoice 10 once", 0)
	ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
	w.put(q, "", ct)
	if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 10 once"}) {
		t.Fatalf("the first delivery: %v", d)
	}
	q.must(`rm "$DR/seen" "$DR/seen-keys"; for k in "$DR"/prekeys/*.agekey; do mv "$k" "$DR/prekeys/old-$(basename "$k")"; done`)
	w.put(q, "", ct) // the server kept its copy
	if d := q.recv(); len(d.messages) != 0 || q.has("seen-keys") {
		t.Fatalf("the server's copy after the state was lost, its key renamed: %v", d)
	}
	q.must(`dr_rekey`)
	if d := q.recv(); len(d.messages) != 0 || d.refused("decrypt_error") != 1 {
		t.Errorf("the server's copy once the mailbox renewed: %v", d)
	}
}

// A key that appears in prekeys/ during a drain, after dr_matched looked,
// decrypts nothing in that drain; the next drain stops on it, and init
// --restore names it, after which it decrypts, as in the Go client
// (TestAKeyMountedDuringADrainDecryptsNothingUntilAdopted).
func TestScriptAKeyMountedDuringADrainDecryptsNothingUntilNamed(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	l := p.line(q.addr, "00000000-0000-4000-8000-0000000000fa", "pay invoice 11 once", 0)
	ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
	id := w.put(q, "", ct)
	if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 11 once"}) {
		t.Fatalf("the first delivery: %v", d)
	}
	held := filepath.Join(w.work, "unmounted")
	q.must(`rm "$DR/seen" "$DR/seen-keys"; mkdir ` + held + `; mv "$DR"/prekeys/*.agekey ` + held + `; dr_rekey`)
	w.put(q, "", ct) // the server kept its copy
	var mounted atomic.Bool
	w.srv.SetMiddle(func(rw http.ResponseWriter, r *http.Request, next http.Handler) {
		// The volume comes back as the message is fetched, after the list.
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/messages/"+id) && mounted.CompareAndSwap(false, true) {
			keys, _ := filepath.Glob(filepath.Join(held, "*.agekey"))
			for _, k := range keys {
				data, err := os.ReadFile(k)
				if err == nil {
					err = os.WriteFile(q.file("prekeys/"+filepath.Base(k)), data, 0o600)
				}
				if err != nil {
					t.Error(err)
				}
			}
		}
		next.ServeHTTP(rw, r)
	})
	if d := q.recv(); !mounted.Load() || len(d.messages) != 0 || d.refused("decrypt_error") != 1 || q.has("seen-floor") {
		t.Fatalf("the drain the volume came back in: %v", d)
	}
	w.honest()
	w.put(q, "", ct) // and serves it again
	if d := q.recv(); d.rc != 4 || len(d.messages) != 0 || w.srv.Count(q.addr) != 1 {
		t.Fatalf("the next drain: %v", d)
	}
	q.must("dr_init --restore > /dev/null")
	if d := q.recv(); !slices.Equal(d.bodies(), []string{"pay invoice 11 once"}) {
		t.Errorf("the drain after init --restore: %v, want the copy delivered again", d)
	}
}

// A sender refuses a prekey statement that ends further ahead of its clock
// than any server takes, 8 days and an hour, as the Go client does
// (TestASenderRefusesAPrekeyThatEndsFurtherAheadThanAnyServerTakes): a stolen
// working key's statement lasts no longer than an honest one (PROTOCOL.md §6).
// Two clocks: the recipient's statement is honest, made 8 days ahead of its
// own clock, and the sender's clock reads more than an hour behind it.
func TestScriptRefusesAPrekeyThatEndsFurtherAheadThanAnyServerTakes(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	behind := at{p, -protocol.MaxSkew - 10*time.Minute}
	if r := behind.sh("printf 'to a prekey ending too far ahead' | dr_send " + q.addr.String()); r.rc == 0 || !strings.Contains(r.err, "further ahead than any server takes") || w.posts(q.addr) != 0 {
		t.Errorf("a send from a clock 70 minutes behind its recipient's: %v, %d posts", r, w.posts(q.addr))
	}
}
