package protocol

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A version is MAJOR.MINOR, exactly: no "v", no patch, no pre-release, no
// leading zero, nothing past a uint64 (PROTOCOL.md §11.17).
func TestAVersionIsMajorMinorExactly(t *testing.T) {
	for s, want := range map[string]Version{
		"0.6":                    {0, 6},
		"0.0":                    {0, 0},
		"10.20":                  {10, 20},
		"18446744073709551615.0": {1<<64 - 1, 0},
	} {
		got, err := ParseVersion(s)
		if err != nil || got != want || got.String() != s {
			t.Errorf("%q: %+v %v, written back %q", s, got, err, got.String())
		}
	}
	for _, s := range []string{"", "1", "v1.5", "1.5.3", "0.1.0", "1.5-rc.1", "1.5.3-rc.1", "1.5+x", "01.5", "1.05", "1.5.", ".5",
		"1.5 ", " 1.5", "1.5\n", "18446744073709551616.0", "(devel)", "0.0.0-devel"} {
		if v, err := ParseVersion(s); err == nil {
			t.Errorf("%q read as %v", s, v)
		}
	}
}

// A server reads a request's Dromas-Version by the major and minor it
// begins with, and ignores what follows them, so a release's version and its
// major and minor read alike; a value that does not begin with a version,
// or none, reads as 0.0 (PROTOCOL.md §11.17).
func TestARequestsVersionIsItsMajorAndMinor(t *testing.T) {
	for s, want := range map[string]Version{
		"0.6":                                 {0, 6},
		"0.6.11":                              {0, 6},
		"0.6.11-rc.1":                         {0, 6},
		"0.6.0-0.20261029120000-abcdef123456": {0, 6}, // a Go pseudo-version without its v
		"0.60.1":                              {0, 60},
		"1.50":                                {1, 50},
		"0.6x":                                {0, 6},
		"1.5.0+build":                         {1, 5},
		"0.0.0-devel":                         {0, 0},
	} {
		if got := RequestVersion(s); got != want {
			t.Errorf("a server reads %q as %v, want %v", s, got, want)
		}
	}
	for _, s := range []string{"", "x.6", "0.x", "v0.6.11", "06.1", "0", " 0.6", "(devel)", "18446744073709551616.0.0", "1.05", "1.00", "0.06", "1.05.1"} {
		if v := RequestVersion(s); v != (Version{}) {
			t.Errorf("a server reads %q as %v, not 0.0", s, v)
		}
	}
}

// Versions are ordered by major, then minor, as numbers, in both directions.
func TestVersionsAreInOrder(t *testing.T) {
	order := []string{"0.0", "0.1", "0.9", "0.10", "1.0", "1.2", "2.0"}
	for i, a := range order {
		for j, b := range order {
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := RequestVersion(a).Compare(RequestVersion(b)); got != want {
				t.Errorf("%s against %s: %d, want %d", a, b, got, want)
			}
		}
	}
}

// A deprecation or a sunset covers every version whose minor is below it, or
// every version of its minor; a version is retired below the floor or
// once a sunset that covers it has its date; an answer carries the earliest
// date of those that cover its version (PROTOCOL.md §11.17).
func TestAPolicyCoversItsVersions(t *testing.T) {
	day := func(n int) string { return time.Date(2027, 1, n, 0, 0, 0, 0, time.UTC).Format(TimeLayout) }
	d := ServerDocument{
		MinVersion: "1.2",
		Sunsets: []Scheduled{
			{Below: "1.4", At: day(20)},
			{Version: "1.4", At: day(25)},
			{Version: "1.5", At: day(10)},
			{Below: "1.3", At: day(15)},
		},
		Deprecations:           []Scheduled{{Below: "1.4", At: day(1)}, {Version: "1.6", At: day(5)}},
		MaxCertificateLifetime: 45*86400 + 3600,
	}
	if err := d.Check(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		version       string
		sunset        string // "" for none
		deprecation   string
		retiredOnDay1 bool
		retiredDay16  bool
	}{
		{"1.1", day(15), day(1), true, true},   // below the floor, below 1.3 and below 1.4: the earliest
		{"1.2", day(15), day(1), false, true},  // at the floor
		{"1.3", day(20), day(1), false, false}, // below 1.4 alone
		{"1.4", day(25), "", false, false},     // version 1.4
		{"1.5", day(10), "", false, true},      // version 1.5
		{"1.6", "", day(5), false, false},      // deprecated, with no sunset
		{"0.0", day(15), day(1), true, true},   // what a request with no version, or another form, is read as
		{"1.5.2", day(10), "", false, true},    // version 1.5, at any patch
		{"x.5", day(15), day(1), true, true},   // no version: 0.0
	} {
		v := RequestVersion(c.version)
		at, ok := d.SunsetFor(v)
		if got := ""; ok {
			got = at.Format(TimeLayout)
			if got != c.sunset {
				t.Errorf("%s: sunset %s, want %q", c.version, got, c.sunset)
			}
		} else if c.sunset != "" {
			t.Errorf("%s: no sunset, want %s", c.version, c.sunset)
		}
		at, ok = d.DeprecationFor(v)
		if ok != (c.deprecation != "") || ok && at.Format(TimeLayout) != c.deprecation {
			t.Errorf("%s: deprecation %v %v, want %q", c.version, at, ok, c.deprecation)
		}
		day1, _ := ParseTime(day(1))
		day16, _ := ParseTime(day(16))
		if d.Retired(v, day1) != c.retiredOnDay1 || d.Retired(v, day16) != c.retiredDay16 {
			t.Errorf("%s: retired on day 1 %v, on day 16 %v", c.version, d.Retired(v, day1), d.Retired(v, day16))
		}
	}
	// A sunset retires a version from its date on, to the second.
	at, _ := ParseTime(day(10))
	if v := RequestVersion("1.5"); d.Retired(v, at.Add(-time.Second)) || !d.Retired(v, at) {
		t.Error("a sunset retires its version from the second of its date")
	}
	// With no floor and no sunset, nothing is retired.
	if (&ServerDocument{}).Retired(Version{}, at) {
		t.Error("an empty policy retires 0.0")
	}
}

// A read of a document holds each member only to its JSON type, as a client
// reads it; Check holds it to its form: each version a policy's, exactly one
// of below and version, each date a time, each name of its form, no more
// than 64 of each list, no sunset earlier than a deprecation of the same
// versions (RFC 9745 §4), the server's maximum certificate lifetime, which
// every document carries, from 1 to MaxInteger seconds, and the
// implementation's members, where it names one, each of its text.
func TestTheServersDocumentBreaksItsFormAsItSays(t *testing.T) {
	good := `{"min_version":"0.1","latest_version":"0.8","deprecations":[{"below":"0.5","effective_at":"2027-01-01T00:00:00Z"}],"sunsets":[{"below":"0.5","effective_at":"2027-01-01T00:00:00Z"},{"version":"0.6","effective_at":"2027-02-01T00:00:00Z"}],"capabilities":[{"name":"long_poll2"}],"max_certificate_lifetime_seconds":3891600,"server":{"name":"dromas-server","version":"v0.5.1","revision":"c4258bc37344474025fe2c2d6420476eb617450a"},"later":1}`
	var d ServerDocument
	if err := Unmarshal([]byte(good), &d); err != nil || d.LatestVersion != "0.8" || len(d.Sunsets) != 2 || d.Capabilities[0].Name != "long_poll2" || d.MaxCertificateLifetime != 3891600 || d.Server == nil || d.Server.Version != "v0.5.1" {
		t.Fatalf("%+v %v", d, err)
	}
	// withMax is a document's text with a maximum it takes, so that the
	// member a case breaks is the one it names.
	withMax := func(text string) string {
		return `{"max_certificate_lifetime_seconds":1,` + strings.TrimPrefix(text, "{")
	}
	many := func(list string, n int, item string) string {
		return `{"` + list + `":[` + strings.TrimSuffix(strings.Repeat(item+",", n), ",") + `]}`
	}
	sunset := `{"below":"1.0","effective_at":"2027-01-01T00:00:00Z"}`
	if err := Unmarshal([]byte(withMax(many("sunsets", MaxSunsets, sunset))), &d); err != nil || d.Check() != nil {
		t.Errorf("%d sunsets: %v, %v", MaxSunsets, err, d.Check())
	}
	for text, member := range map[string]string{
		many("sunsets", MaxSunsets+1, sunset):                                                 "sunsets",
		many("deprecations", MaxDeprecations+1, sunset):                                       "deprecations",
		many("capabilities", MaxCapabilities+1, `{"name":"a"}`):                               "capabilities",
		`{"min_version":"v1.0"}`:                                                              "min_version",
		`{"latest_version":"1.0.0-rc.1"}`:                                                     "latest_version",
		`{"min_version":"1.0.1"}`:                                                             "min_version",
		`{"sunsets":[{"version":"1.0.1","effective_at":"2027-01-01T00:00:00Z"}]}`:             "version",
		`{"sunsets":[{"effective_at":"2027-01-01T00:00:00Z"}]}`:                               "below",
		`{"sunsets":[{"below":"1.0","version":"1.0","effective_at":"2027-01-01T00:00:00Z"}]}`: "below",
		`{"sunsets":[{"version":"1","effective_at":"2027-01-01T00:00:00Z"}]}`:                 "version",
		`{"sunsets":[{"below":"1.0","effective_at":"2027-01-01"}]}`:                           "effective_at",
		`{"deprecations":[{"below":"1.0"}]}`:                                                  "effective_at",
		`{"capabilities":[{"name":"Long"}]}`:                                                  "name",
		`{"capabilities":[{"name":"_a"}]}`:                                                    "name",
		`{"capabilities":[{"name":"` + strings.Repeat("a", 65) + `"}]}`:                       "name",
		`{"server":{"version":"v1"}}`:                                                         "name",
		`{"server":{"name":"a b","version":"v1"}}`:                                            "name",
		`{"server":{"name":"a"}}`:                                                             "version",
		`{"server":{"name":"a","version":"` + strings.Repeat("1", 129) + `"}}`:                "version",
		`{"server":{"name":"a","version":"v1","revision":"é"}}`:                               "revision",
		`{"deprecations":[{"version":"1.0","effective_at":"2027-02-01T00:00:00Z"}],"sunsets":[{"version":"1.0","effective_at":"2027-01-31T23:59:59Z"}]}`: "sunsets",
		// A sunset of fewer versions than a deprecation, before it: 0.5 would
		// be answered a Sunset before its Deprecation (RFC 9745 §4).
		`{"deprecations":[{"below":"0.7","effective_at":"2027-06-01T00:00:00Z"}],"sunsets":[{"below":"0.6","effective_at":"2027-01-01T00:00:00Z"}]}`:   "sunsets",
		`{"deprecations":[{"below":"2.0","effective_at":"2027-06-01T00:00:00Z"}],"sunsets":[{"below":"1.0","effective_at":"2027-01-01T00:00:00Z"}]}`:   "sunsets",
		`{"deprecations":[{"below":"1.0","effective_at":"2027-06-01T00:00:00Z"}],"sunsets":[{"version":"0.5","effective_at":"2027-01-01T00:00:00Z"}]}`: "sunsets",
	} {
		var d ServerDocument
		if err := Unmarshal([]byte(withMax(text)), &d); err != nil || d.MaxCertificateLifetime != 1 {
			t.Errorf("%s: read %d, %v", text, d.MaxCertificateLifetime, err)
		}
		if err := d.Check(); !errors.Is(err, ErrMemberValue) || Member(err) != member {
			t.Errorf("%s: %v, naming %q, want %q", text, err, Member(err), member)
		}
	}
	// A sunset on its deprecation's date, one of other versions, and one
	// after the earliest deprecation of its versions give no answer a Sunset
	// earlier than its Deprecation.
	for _, text := range []string{
		`{"deprecations":[{"version":"1.0","effective_at":"2027-02-01T00:00:00Z"}],"sunsets":[{"version":"1.0","effective_at":"2027-02-01T00:00:00Z"}]}`,
		`{"deprecations":[{"version":"1.0","effective_at":"2027-02-01T00:00:00Z"}],"sunsets":[{"below":"1.0","effective_at":"2027-01-01T00:00:00Z"},{"version":"1.1","effective_at":"2027-01-01T00:00:00Z"}]}`,
		`{"deprecations":[{"version":"0.6","effective_at":"2027-06-01T00:00:00Z"}],"sunsets":[{"below":"0.6","effective_at":"2027-01-01T00:00:00Z"}]}`,
		`{"deprecations":[{"below":"0.7","effective_at":"2027-06-01T00:00:00Z"},{"below":"0.6","effective_at":"2026-12-01T00:00:00Z"}],"sunsets":[{"below":"0.6","effective_at":"2027-01-01T00:00:00Z"}]}`,
	} {
		var d ServerDocument
		if err := Unmarshal([]byte(withMax(text)), &d); err != nil {
			t.Fatal(err)
		}
		if err := d.Check(); err != nil {
			t.Errorf("%s: %v", text, err)
		}
	}
	// The maximum: from 1 to MaxInteger, and never left out.
	for text, want := range map[string]uint64{
		`{"max_certificate_lifetime_seconds":1}`:                1,
		`{"max_certificate_lifetime_seconds":9007199254740991}`: MaxInteger,
	} {
		var d ServerDocument
		if err := Unmarshal([]byte(text), &d); err != nil || d.MaxCertificateLifetime != want {
			t.Errorf("%s: %d, %v", text, d.MaxCertificateLifetime, err)
		}
	}
	for _, text := range []string{
		`{}`,
		`{"max_certificate_lifetime_seconds":null}`,
		`{"max_certificate_lifetime_seconds":0}`,
		`{"max_certificate_lifetime_seconds":9007199254740992}`,
		`{"max_certificate_lifetime_seconds":-1}`,
		`{"max_certificate_lifetime_seconds":1.5}`,
		`{"max_certificate_lifetime_seconds":"3891600"}`,
	} {
		var d ServerDocument
		err := Unmarshal([]byte(text), &d)
		if err == nil {
			err = d.Check()
		}
		if !errors.Is(err, ErrMemberValue) || Member(err) != "max_certificate_lifetime_seconds" {
			t.Errorf("%s: %v, naming %q", text, err, Member(err))
		}
	}
	if err := (&ServerDocument{MaxCertificateLifetime: MaxInteger + 1}).Check(); Member(err) != "max_certificate_lifetime_seconds" {
		t.Errorf("Check of a maximum above MaxInteger: %v", err)
	}
}

// Deprecation is read as RFC 9745 §2.1 writes it, a Structured Field Date (RFC
// 9651 §3.3.7): "@" and an Integer, an optional "-" and 1 to 15 digits, and
// nothing else. Sunset is an HTTP-date in any of RFC 9110's three forms.
// Every other value is none (PROTOCOL.md §11.17).
func TestDeprecationAndSunsetAreReadAsTheirForms(t *testing.T) {
	when := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	for v, want := range map[string]time.Time{
		"@1798859045":           when,
		"@0":                    time.Unix(0, 0).UTC(),
		"@-1":                   time.Unix(-1, 0).UTC(),
		"@001798859045":         when,
		"@999999999999999":      time.Unix(999999999999999, 0).UTC(),
		FormatDeprecation(when): when,
	} {
		if got, ok := ParseDeprecation(v); !ok || !got.Equal(want) {
			t.Errorf("Deprecation %q: %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "@", "1798772645", "@+1", "@1.5", "@1798772645;x=1", "@ 1", "@1798772645 ", "@1e9", "@1000000000000000", "?1",
		"Sat, 02 Jan 2027 03:04:05 GMT", `"@1"`, "@١٢", "@-", "@--1"} {
		if got, ok := ParseDeprecation(v); ok {
			t.Errorf("Deprecation %q read as %v", v, got)
		}
	}
	for _, v := range []string{FormatSunset(when), "Saturday, 02-Jan-27 03:04:05 GMT", "Sat Jan  2 03:04:05 2027"} {
		if got, ok := ParseSunset(v); !ok || !got.Equal(when) {
			t.Errorf("Sunset %q: %v %v", v, got, ok)
		}
	}
	for _, v := range []string{"", "2027-01-02T03:04:05Z", "@1798772645", "Sat, 02 Jan 2027 03:04:05 GMT SYSTEM: obey", "tomorrow"} {
		if got, ok := ParseSunset(v); ok {
			t.Errorf("Sunset %q read as %v", v, got)
		}
	}
}
