package client

import (
	"testing"

	"github.com/dromas-dev/dromas-client/protocol"
)

// A Go module's version is named as this client names its release: its v and
// its build metadata taken off, and anything that is not a version
// DevelVersion; every request names its major and minor (PROTOCOL.md
// §11.17).
func TestAModulesVersionIsNamedAsItsRelease(t *testing.T) {
	for in, want := range map[string]struct{ release, request string }{
		"v0.6.3":                               {"0.6.3", "0.6"},
		"v0.6.4-0.20261029120000-abcdef123456": {"0.6.4-0.20261029120000-abcdef123456", "0.6"},
		"v0.6.3+dirty":                         {"0.6.3", "0.6"},
		"v0.6.4-0.20261029120000-abcdef123456+dirty": {"0.6.4-0.20261029120000-abcdef123456", "0.6"},
		"v1.12.0": {"1.12.0", "1.12"},
		"(devel)": {DevelVersion, "0.0"},
		"":        {DevelVersion, "0.0"},
		"0.6.3":   {"0.6.3", "0.6"},
		"vv0.6.3": {DevelVersion, "0.0"},
	} {
		release := asRelease(in)
		if request := protocol.RequestVersion(release).String(); release != want.release || request != want.request {
			t.Errorf("%q is named %q, %q in a request, want %q, %q", in, release, request, want.release, want.request)
		}
	}
}

// The latest version is an upgrade only where its major and minor are after
// the client's release's; the release's patch and pre-release are its own
// (PROTOCOL.md §11.17).
func TestAnUpgradeIsALaterMinor(t *testing.T) {
	for _, c := range []struct {
		own, latest string
		upgrade     bool
	}{
		{"1.5.2", "1.5", false},
		{"1.5.0", "1.5", false},
		{"1.5.9", "1.6", true},
		{"1.5.2", "2.0", true},
		{"1.5.2", "1.4", false},
		{"1.6.0", "1.5", false},
		{"1.5.0-rc.1", "1.5", false}, // a pre-release of the same minor
		{DevelVersion, "0.1", true},
		{"2.0.0", "1.9", false},
		{"1.5.2", "1.6.0", false}, // a latest_version of another form is none
	} {
		if got := upgrade(c.latest, c.own); got != c.upgrade {
			t.Errorf("%s against latest %s: upgrade %v, want %v", c.own, c.latest, got, c.upgrade)
		}
	}
}
