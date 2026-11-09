package sh_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/internal/forge"
)

// shSignals are the ways the script's records stop matching its prekeys/, as
// the Go client's do (signals in client/restore_test.go), each made from a
// mailbox q that has delivered a message from p, which the server kept a copy
// of: seen and seen-keys lost with prekeys/ kept.
var shSignals = []struct{ name, make string }{
	{"seen-keys lost", `rm "$DR/seen" "$DR/seen-keys"`},
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
			if d := q.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"pay invoice 14 once"}) {
				t.Fatalf("the first delivery: %v", d)
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

// init --restore acknowledges each signal and renews. The failure's cost is
// accepted: the next drain delivers again the copy the server kept, and a
// message to the new prekey is delivered.
func TestScriptInitRestoreAcknowledgesAndRenews(t *testing.T) {
	for _, s := range shSignals {
		t.Run(s.name, func(t *testing.T) {
			w := newWorld(t)
			p, q := w.mailbox(), w.mailbox()
			l := p.line(q.addr, "00000000-0000-4000-8000-0000000000e2", "pay invoice 15 once", 0)
			ct := forge.Encrypt(t, w.srv, q.addr, l, p.sign(q.addr, l))
			w.put(q, "", ct)
			if d := q.recv(); d.rc != 0 || len(d.messages) != 1 {
				t.Fatalf("the first delivery: %v", d)
			}
			q.must(s.make)
			w.put(q, "", ct) // the server kept its copy
			serial := q.read("serial")
			if out := q.must("dr_init --restore"); !strings.Contains(out, `"type":"restore","acknowledged":true`) || q.read("serial") == serial {
				t.Fatalf("init --restore: %q, serial %q from %q: it renewed nothing", out, q.read("serial"), serial)
			}
			if d := q.recv(); !slices.Equal(d.bodies(), []string{"pay invoice 15 once"}) {
				t.Fatalf("the server's copy after init --restore: %v, want it delivered again", d)
			}
			p.mustSend(q, "to the new prekey")
			if d := q.recv(); !slices.Equal(d.bodies(), []string{"to the new prekey"}) {
				t.Errorf("a message to the new prekey: %v", d)
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
