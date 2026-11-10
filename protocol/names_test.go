package protocol

import (
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/internal/codename"
)

// Every code the API answers and every refusal this package makes is named
// by the convention the standards share (internal/codename): the codes from
// Codes(), the refusals read from this package's source, so a new one is
// held as it is added (dromas-client#845).
func TestCodeNames(t *testing.T) {
	for _, c := range Codes() {
		if err := codename.Check(string(c)); err != nil {
			t.Error(err)
		}
	}
	refusals, err := codename.Consts(".", func(n string) bool { return strings.HasPrefix(n, "Refused") })
	if err != nil {
		t.Fatal(err)
	}
	if len(refusals) == 0 {
		t.Fatal("no refusal read from the source")
	}
	for name, c := range refusals {
		if err := codename.Check(c); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
