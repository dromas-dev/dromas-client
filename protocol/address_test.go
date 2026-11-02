package protocol

import (
	"strings"
	"testing"
)

// One text per address: Go's decoder skips a line feed and a carriage return, even
// strict, so length alone would let another text through.
func TestAnAddressHasOneText(t *testing.T) {
	if _, err := ParseAddress("d_" + strings.Repeat("A", 64)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"d_" + strings.Repeat("A", 63) + "\n",
		"d_" + strings.Repeat("A", 63) + "\r",
		"d_" + "\n" + strings.Repeat("A", 63),
		"d_" + strings.Repeat("A", 62) + "\r\n",
		"d_" + strings.Repeat("A", 63) + "=",
		"d_" + strings.Repeat("A", 63) + "+",
		"d_" + strings.Repeat("A", 63) + "/",
	} {
		if a, err := ParseAddress(bad); err == nil {
			t.Errorf("%q read as %s", bad, a)
		}
	}
}
