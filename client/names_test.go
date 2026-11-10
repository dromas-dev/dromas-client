package client_test

import (
	"strings"
	"testing"

	"github.com/dromas-dev/dromas-client/client"
	"github.com/dromas-dev/dromas-client/internal/codename"
)

// Every code this package gives a refusal or an error line is named by the
// convention the standards share (internal/codename), read from its source:
// the refusals, RestoreRequired and IdentityLocked, and a FormatError's two
// (dromas-client#845).
func TestCodeNames(t *testing.T) {
	consts, err := codename.Consts(".", func(n string) bool {
		return strings.HasPrefix(n, "Refused") || n == "RestoreRequired" || n == "IdentityLocked"
	})
	if err != nil {
		t.Fatal(err)
	}
	if consts["RestoreRequired"] == "" || consts["IdentityLocked"] == "" || len(consts) < 6 {
		t.Fatalf("too few codes read from the source: %v", consts)
	}
	consts["FormatError newer"] = (&client.FormatError{Format: 2, Client: 1}).Code()
	consts["FormatError older"] = (&client.FormatError{Format: 1, Client: 2}).Code()
	for name, c := range consts {
		if err := codename.Check(c); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
