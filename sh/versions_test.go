package sh_test

import (
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// scriptVersion is the version of the protocol the script names in every
// request.
const scriptVersion = "0.6"

// The script names its version in every request (PROTOCOL.md §11.17), and a
// 410 client_version_unsupported is said in the script's words, with
// min_version once it has its form.
func TestTheScriptNamesItsVersion(t *testing.T) {
	needEveryType(t)
	w := newWorld(t)
	b := w.mailbox()
	script := abs(t, "dromas.sh")
	if r := b.sh("bash " + script + " recv"); r.rc != 0 {
		t.Fatalf("recv: %v", r)
	}
	for _, q := range w.srv.Requests() {
		if q.Version != scriptVersion {
			t.Errorf("%s %s names %q, not %q", q.Method, q.Path, q.Version, scriptVersion)
		}
	}

	// Retired: the script's words, the floor once it has its form.
	w.srv.SetPolicy(protocol.ServerDocument{MinVersion: "0.7"})
	r := b.sh("bash " + script + " recv")
	if r.rc == 0 || !strings.Contains(r.err, `"status":410,"code":"client_version_unsupported","min_version":"0.7"`) ||
		!strings.Contains(r.err, "this server no longer takes this client's version, 0.6: upgrade this client; the oldest it takes is 0.7") {
		t.Fatalf("recv at a server that retired its version: %v", r)
	}
}
