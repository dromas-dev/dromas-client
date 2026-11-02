package protocol

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// The types every mailbox must take are exactly the table of PROTOCOL.md §4.1, so a
// type added without its optional mark does not become one every server posts to
// mailboxes that cannot read it.
func TestTheMandatoryTypesAreTheSpecsTable(t *testing.T) {
	spec, err := os.ReadFile("../PROTOCOL.md")
	if err != nil {
		t.Fatal(err)
	}
	var table []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z0-9@.-]+)` \\| [0-9]+ bytes \\| the ").FindAllSubmatch(spec, -1) {
		table = append(table, string(m[1]))
	}
	var mandatory []string
	for _, kt := range IdentityTypes() {
		if !identityTypes[kt].optional {
			mandatory = append(mandatory, kt)
		}
	}
	slices.Sort(table)
	slices.Sort(mandatory)
	if len(table) == 0 || !slices.Equal(table, mandatory) {
		t.Errorf("§4.1 defines %v, and the code takes %v from every sender", table, mandatory)
	}
}
