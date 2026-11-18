package sh_test

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// The script's armour reader takes no signature longer than the protocol's
// bound: it writes its own number, so nothing else ties the two together.
func TestTheScriptsBoundIsTheProtocols(t *testing.T) {
	script, err := os.ReadFile("dromas.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`dr_blob\(\)\{[^\n]*\n[^\n]*\n\s*\[ "\$\(wc -c < "\$1"\)" -le ` + strconv.Itoa(protocol.MaxSignature) + ` \]`).Match(script) {
		t.Errorf("dr_blob does not hold a signature to %d", protocol.MaxSignature)
	}
}
