package sh_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dromas-dev/dromas-client/internal/forge"
	"github.com/dromas-dev/dromas-client/protocol"
)

// shSignals are the two ways the script's records stop matching its prekeys/,
// as the Go client's do (signals in client/restore_test.go), each made from a
// mailbox q that has delivered a message from p, which the server kept a copy
// of: seen and seen-keys lost with prekeys/ kept, and seen and generation put
// back from a copy older than the drain that delivered it.
var shSignals = []struct{ name, make string }{
	{"seen-keys lost", `rm "$DR/seen" "$DR/seen-keys"`},
	{"records older than a drain", `cp "$W/old/seen-keys" "$DR/seen-keys"; rm -f "$DR/seen" "$DR/generation"`},
}

// Each signal stops every command of the script that receives, sends or
// renews, init among them, with exit 4 and a fatal error line of the code
// state_not_the_keys, before anything is listed, fetched or acked; addr still
// runs. Records that match are the control.
func TestScriptStopsEveryCommandOnRecordsThatDoNotMatchItsPrekeys(t *testing.T) {
	for _, s := range append(shSignals, struct{ name, make string }{"control: records that match", ":"}) {
		t.Run(s.name, func(t *testing.T) {
			w := newWorld(t)
			p, q := w.mailbox(), w.mailbox()
			q.must(`mkdir "$W/old" && cp "$DR/seen-keys" "$W/old/"`)
			l := p.line(q.addr, "00000000-0000-4000-8000-0000000000e1", "pay invoice 14 once", 0)
			ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
			w.put(q, "", ct)
			if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 14 once"}) || q.read("generation") != "1\n" || q.read("prekeys/generation") != "1\n" {
				t.Fatalf("the first delivery: %v; generation %q, prekeys/generation %q", d, q.read("generation"), q.read("prekeys/generation"))
			}
			q.must(s.make)
			w.put(q, "", ct) // the server kept its copy
			control := strings.HasPrefix(s.name, "control")
			for _, c := range []string{"dr_recv", "dr_send " + p.addr.String() + " /dev/null", "dr_rekey", "dr_init"} {
				before := len(w.srv.Requests())
				r := q.sh(c)
				switch {
				case control && r.rc == 4:
					t.Errorf("%s on records that match: %v", c, r)
				case control:
				case r.rc != 4 || !strings.Contains(r.err, `"fatal":true,"code":"state_not_the_keys"`) || len(w.srv.Requests()) != before || w.srv.Count(q.addr) != 1:
					t.Errorf("%s: %v, with %d calls", c, r, len(w.srv.Requests())-before)
				}
			}
			if r := q.sh("dr_addr"); r.rc != 0 {
				t.Errorf("addr: %v", r)
			}
		})
	}
}

// init --restore acknowledges each signal: the next drain refuses the copy
// the server kept as stale, raising the floor by its list's pass, and a
// message sent past the floor's edge is delivered.
func TestScriptInitRestoreAcknowledgesAndTheFloorHolds(t *testing.T) {
	for _, s := range shSignals {
		t.Run(s.name, func(t *testing.T) {
			w := newWorld(t)
			p, q := w.mailbox(), w.mailbox()
			q.must(`mkdir "$W/old" && cp "$DR/seen-keys" "$W/old/"`)
			l := p.line(q.addr, "00000000-0000-4000-8000-0000000000e2", "pay invoice 15 once", 0)
			ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
			w.put(q, "", ct)
			if d := q.recv(); d.rc != 0 || len(d.messages) != 1 {
				t.Fatalf("the first delivery: %v", d)
			}
			q.must(s.make)
			w.put(q, "", ct) // the server kept its copy
			if out := q.must("dr_init --restore"); !strings.Contains(out, `"type":"restore","acknowledged":true`) || !q.has("floor-owed") || q.has("pass") {
				t.Fatalf("init --restore: %q", out)
			}
			if d := q.recv(); len(d.messages) != 0 || d.refused("stale") != 1 || q.has("floor-owed") || q.read("generation") != q.read("prekeys/generation") {
				t.Fatalf("the server's copy after init --restore: %v", d)
			}
			f, err := strconv.ParseInt(strings.TrimSpace(q.read("seen-floor")), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			edge := time.Unix(f, 0).Add(protocol.TTL)
			at := p.line(q.addr, "00000000-0000-4000-8000-0000000000e3", "at the floor's edge", time.Since(edge))
			w.inject(q, "", at, p.sign(q.addr, at))
			if d := q.recv(); !slices.Equal(d.bodies(), []string{"at the floor's edge"}) {
				t.Errorf("a message at the floor's edge: %v", d)
			}
		})
	}
}

// init --restore on records that match changes nothing, so it may run at
// every start: it says it acknowledged nothing, and a message sent since is
// delivered.
func TestScriptInitRestoreOnRecordsThatMatchChangesNothing(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	p.mustSend(q, "delivered before")
	q.recv()
	before := q.must(`find "$DR" -type f ! -name 'pass*' | sort | xargs sha256sum`)
	if out := q.must("dr_init --restore"); !strings.Contains(out, `"type":"restore","acknowledged":false`) {
		t.Fatalf("init --restore: %q", out)
	}
	if after := q.must(`find "$DR" -type f ! -name 'pass*' | sort | xargs sha256sum`); after != before {
		t.Fatalf("init --restore changed the mailbox:\n%s\nto\n%s", before, after)
	}
	p.mustSend(q, "sent since")
	if d := q.recv(); !slices.Equal(d.bodies(), []string{"sent since"}) {
		t.Fatalf("a message sent since: %v", d)
	}
}

// A drain killed between generation and prekeys/generation leaves the file
// behind, never ahead: the next drain raises no alarm and writes it again. A
// missing prekeys/generation reads forward the same way, owing nothing.
func TestScriptACountBehindOrMissingRaisesNoAlarm(t *testing.T) {
	w := newWorld(t)
	p, q := w.mailbox(), w.mailbox()
	p.mustSend(q, "delivered before")
	q.recv()
	for _, c := range []struct{ name, make string }{
		{"behind", `printf '2\n' > "$DR/generation"`},
		{"missing", `rm "$DR/prekeys/generation"`},
	} {
		q.must(c.make)
		want := q.read("generation")
		p.mustSend(q, "sent with prekeys/generation "+c.name)
		if d := q.recv(); d.rc != 0 || len(d.messages) != 1 || q.has("floor-owed") {
			t.Fatalf("a drain with prekeys/generation %s: %v", c.name, d)
		}
		if got := q.read("prekeys/generation"); got != q.read("generation") || got == want {
			t.Fatalf("with prekeys/generation %s, after the drain: %q, generation %q", c.name, got, q.read("generation"))
		}
	}
}
