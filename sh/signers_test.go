package sh_test

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
)

// The script's mailbox, whose identity signs, beside Go mailboxes that sign
// by their identity and under certificates (PROTOCOL.md §5): it writes to and
// reads from both, takes either signer of each, and follows each as it
// renews, by (c, p).

// goBodies drains Go mailbox g, and returns the bodies it delivered, sorted,
// as their JSON strings.
func goBodies(t *testing.T, g *client.Mailbox) []string {
	t.Helper()
	res, err := g.Receive(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused) != 0 {
		t.Errorf("the Go mailbox refused %+v", res.Refused)
	}
	var bodies []string
	for _, d := range res.Delivered {
		bodies = append(bodies, string(d.Envelope.Body))
	}
	slices.Sort(bodies)
	return bodies
}

// A script mailbox and a Go mailbox exchange messages with the Go mailbox's
// identity signing, as init makes it, and under certificates, as init
// --offline makes it, through a renewal of each.
func TestTheScriptAndAGoMailboxOfEachSigner(t *testing.T) {
	for _, signer := range []string{"identity", "certificate"} {
		t.Run(signer, func(t *testing.T) {
			w := newWorld(t)
			ctx := t.Context()
			s := w.mailbox()
			g, gb := w.goMailbox(signer == "certificate")
			exchange := func(when string) {
				t.Helper()
				s.mustSend(gb, "from the script, "+when)
				if got := goBodies(t, g); !slices.Equal(got, []string{`"from the script, ` + when + `"`}) {
					t.Errorf("%s: the Go mailbox read %q", when, got)
				}
				if _, err := g.Send(ctx, s.addr, "from Go, "+when); err != nil {
					t.Fatalf("%s: %v", when, err)
				}
				if d := s.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"from Go, " + when}) {
					t.Errorf("%s: the script read %v", when, d)
				}
			}
			exchange("first")
			peer := "peers/" + gb.addr.String() + "/"
			if _, rank := w.served(gb); s.read(peer+"rank") != strconv.FormatUint(rank, 10)+"\n" || (rank == 0) != (signer == "identity") {
				t.Errorf("the script recorded the Go mailbox's statement by a signer of rank %q, served by one of %d", s.read(peer+"rank"), rank)
			}
			if _, err := g.Rekey(ctx); err != nil {
				t.Fatal(err)
			}
			exchange("after Go's renewal")
			s.must("dr_rekey")
			exchange("after the script's renewal")
		})
	}
}

// A Go mailbox whose identity signs renews under a certificate (dromas cert
// switch): the script takes what the identity signed before, read after,
// and the first message under the certificate, by one read of the sender in
// the drain; its sends follow the statement under the certificate, which
// stands above every one the identity signed, so a server that serves the
// identity's again is refused it (PROTOCOL.md §5.3).
func TestTheScriptFollowsAGoMailboxUnderACertificate(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	s := w.mailbox()
	g, gb := w.goMailbox(false)
	s.mustSend(gb, "to the identity's statement")
	if got := goBodies(t, g); !slices.Equal(got, []string{`"to the identity's statement"`}) {
		t.Fatalf("the Go mailbox read %q", got)
	}
	byIdentity := w.prekey(gb)
	if _, err := g.Send(ctx, s.addr, "by the identity, read after the renewal"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RenewUnderCertificate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, rank := w.served(gb); rank == 0 {
		t.Fatal("the renewal published no statement under a certificate")
	}
	if _, err := g.Send(ctx, s.addr, "under the first certificate"); err != nil {
		t.Fatal(err)
	}
	n := len(w.srv.Requests())
	d := s.recv()
	reads := 0
	for _, c := range w.calls(n) {
		if c == http.MethodGet+" /v1/"+gb.addr.String() {
			reads++
		}
	}
	if d.rc != 0 || !slices.Equal(slices.Sorted(slices.Values(d.bodies())), []string{"by the identity, read after the renewal", "under the first certificate"}) || strings.Contains(d.said, "REFUSED") || reads != 1 {
		t.Errorf("the identity's message and the certificate's, in one drain: %v, %d reads of the sender", d, reads)
	}
	s.mustSend(gb, "to the statement under the certificate")
	peer := "peers/" + gb.addr.String() + "/"
	if _, rank := w.served(gb); s.read(peer+"rank") != strconv.FormatUint(rank, 10)+"\n" {
		t.Errorf("the script recorded rank %q, not the certificate's, %d", s.read(peer+"rank"), rank)
	}
	if got := goBodies(t, g); !slices.Equal(got, []string{`"to the statement under the certificate"`}) {
		t.Errorf("the Go mailbox read %q", got)
	}
	w.serving(gb, byIdentity)
	if r := s.send(gb.addr.String(), "not to the identity's statement"); r.rc == 0 || !strings.Contains(r.err, "rollback: "+gb.addr.String()+"'s prekey is signed by a signer of rank 0, below one already seen") || !s.bare() {
		t.Errorf("the identity's statement, served after one under a certificate: %v", r)
	}
}

// A message the Go mailbox's identity signed is taken from a sender whose
// certificates a reader holds revoked, and one under a revoked certificate
// is not: a revocation of certificates covers certificates alone (PROTOCOL.md
// §10.2).
func TestTheScriptTakesTheIdentityPastARevocationOfCertificates(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	s := w.mailbox()
	g, gb := w.goMailbox(true)
	if _, err := g.Send(ctx, s.addr, "under the first certificate"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Rekey(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RevokeEarlier(ctx); err != nil {
		t.Fatal(err)
	}
	line := envelope(t, gb.addr, s.addr, []byte(gb.read("identity.doc")), "00000000-0000-4000-8000-0000000000e1", "by the identity", 0)
	w.inject(s, "", line, w.signWith(gb.file("identity"), s.addr, line))
	if d := s.recv(); d.rc != 0 || !slices.Equal(d.bodies(), []string{"by the identity"}) || d.refused("certificate_revoked") != 1 || !s.has("peers/"+gb.addr.String()+"/revoked-certs.json") {
		t.Errorf("the identity's message and one under a revoked certificate: %v", d)
	}
}
