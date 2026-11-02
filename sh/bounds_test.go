package sh_test

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// The script's bound on a signature of each key type is the protocol's, and its
// armour's reader takes none longer than the largest: it writes its own numbers,
// so nothing else ties them together.
func TestTheScriptsBoundsAreTheProtocols(t *testing.T) {
	script, err := os.ReadFile("dromas.sh")
	if err != nil {
		t.Fatal(err)
	}
	most := 0
	for _, kt := range protocol.IdentityTypes() {
		bound := protocol.SignatureBound(kt)
		most = max(most, bound)
		if !regexp.MustCompile(regexp.QuoteMeta(kt) + `\) echo ` + strconv.Itoa(bound) + ` ;;`).Match(script) {
			t.Errorf("dr_sigmax does not hold %s to %d", kt, bound)
		}
	}
	if !regexp.MustCompile(`dr_blob\(\)\{[^\n]*\n[^\n]*\n\s*\[ "\$\(wc -c < "\$1"\)" -le ` + strconv.Itoa(most) + ` \]`).Match(script) {
		t.Errorf("dr_blob does not hold a signature to %d", most)
	}
}
