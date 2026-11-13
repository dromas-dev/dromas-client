package protocol

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// TestRateLimitReadsEachMember holds ParseRateLimit to PROTOCOL.md §11.5:
// each field, one line each unless split at "\n", and the budgets it gives.
func TestRateLimitReadsEachMember(t *testing.T) {
	sends := Budget{Left: 37, Cap: 60, Refill: 50, Window: DefaultWindow, Full: 39000}
	cases := []struct {
		field string
		want  RateLimit
	}{
		{"sends=37;c=60;q=50;f=39000", RateLimit{QuotaSends: sends}},
		{"sends=37;f=39000;q=50;c=60", RateLimit{QuotaSends: sends}},
		{"sends=37;c=60;q=50;w=86400;f=39000", RateLimit{QuotaSends: sends}},
		{"sends=3;c=10;q=4;w=60;f=10", RateLimit{QuotaSends: {Left: 3, Cap: 10, Refill: 4, Window: 60, Full: 10}}},
		{"sends=0;c=1;q=1;f=0", RateLimit{QuotaSends: {Cap: 1, Refill: 1, Window: DefaultWindow}}},
		{"sends=37;c=60;q=50;f=39000;later=?1", RateLimit{QuotaSends: sends}},
		{"later=5;c=6;q=1;f=0, sends=37;c=60;q=50;f=39000", RateLimit{"later": {Left: 5, Cap: 6, Refill: 1, Window: DefaultWindow}, QuotaSends: sends}},
		{"other=\"x\", sends=37;c=60;q=50;f=39000", RateLimit{QuotaSends: sends}},
		{"sends=1;c=60;q=50;f=1\nsends=37;c=60;q=50;f=39000", RateLimit{QuotaSends: sends}},
		// A member of another form is left out alone.
		{"sends=37;q=50;f=39000", RateLimit{}},
		{"sends=37;c=60;f=39000", RateLimit{}},
		{"sends=37;c=60;q=50", RateLimit{}},
		{"sends=37.0;c=60;q=50;f=39000", RateLimit{}},
		{"sends=37;c=60.0;q=50;f=39000", RateLimit{}},
		{"sends=37;c=60;q=50;w=1.5;f=39000", RateLimit{}},
		{"sends=\"37\";c=60;q=50;f=39000", RateLimit{}},
		{"sends=(37);c=60;q=50;f=39000", RateLimit{}},
		{"sends;c=60;q=50;f=39000", RateLimit{}},
		{"sends=-1;c=60;q=50;f=39000", RateLimit{}},
		{"sends=61;c=60;q=50;f=0", RateLimit{}},
		{"sends=0;c=0;q=50;f=0", RateLimit{}},
		{"sends=0;c=1;q=0;f=0", RateLimit{}},
		{"sends=0;c=1;q=1;w=0;f=0", RateLimit{}},
		{"sends=0;c=1;q=1;f=-1", RateLimit{}},
		{"sends=37;c=60;q=50;f=39000, other=1", RateLimit{QuotaSends: sends}},
		// A field that is no Dictionary, or none, is no reading.
		{"", nil},
		{"sends=37;c=60;q=50;f=39000,", nil},
		{"Sends=37;c=60;q=50;f=39000", nil},
		{"{\"sends\":{\"r\":37}}", nil},
		{"sends=1000000000000000;c=1000000000000000;q=1;f=0", nil},
	}
	for _, c := range cases {
		var lines []string
		if c.field != "" {
			lines = strings.Split(c.field, "\n")
		}
		got := ParseRateLimit(lines)
		if (got == nil) != (c.want == nil) || !maps.Equal(got, c.want) {
			t.Errorf("%q: %v, want %v", c.field, got, c.want)
		}
	}
}

// TestRateLimitWritesWhatItReads holds String to §11.5's form: the members
// in key order, w only where it is not a day, and what is no valid budget
// left out; and ParseRateLimit to reading it back as it was.
func TestRateLimitWritesWhatItReads(t *testing.T) {
	r := RateLimit{
		QuotaSends: {Left: 37, Cap: 60, Refill: 50, Window: DefaultWindow, Full: 39000},
		"bad":      {Left: 2, Cap: 1, Refill: 1, Window: DefaultWindow},
		"Upper":    {Left: 1, Cap: 1, Refill: 1, Window: DefaultWindow},
		"calls":    {Left: 3, Cap: 10, Refill: 4, Window: 60, Full: 10},
	}
	const want = "calls=3;c=10;q=4;w=60;f=10, sends=37;c=60;q=50;f=39000"
	if got := r.String(); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	back := ParseRateLimit([]string{want})
	delete(r, "bad")
	delete(r, "Upper")
	if !maps.Equal(back, r) {
		t.Fatalf("read back as %v, want %v", back, r)
	}
	if got := (RateLimit{}).String(); got != "" {
		t.Fatalf("no budget written as %q", got)
	}
}

// TestRateLimitsIntervalIsItsWindowOverItsRefill holds Interval to §11.5's
// T = w/q, the example's 1728 s among them.
func TestRateLimitsIntervalIsItsWindowOverItsRefill(t *testing.T) {
	for _, c := range []struct {
		b    Budget
		want time.Duration
	}{
		{Budget{Refill: 50, Window: DefaultWindow}, 1728 * time.Second},
		{Budget{Refill: 4, Window: 60}, 15 * time.Second},
		{Budget{Refill: 7, Window: 1}, time.Second / 7},
	} {
		if got := c.b.Interval(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.b, got, c.want)
		}
	}
}

// FuzzParseRateLimit holds ParseRateLimit to its doc comment on any field:
// no panic, every budget it gives valid, and what it gives written by String
// and read again unchanged.
func FuzzParseRateLimit(f *testing.F) {
	for _, s := range []string{"sends=37;c=60;q=50;f=39000", "sends=3;c=10;q=4;w=60;f=10, other=?0", "a=1;c=1;q=1;f=1;f=2", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, field string) {
		r := ParseRateLimit([]string{field})
		for q, b := range r {
			if !b.valid() {
				t.Fatalf("%q gave %q an invalid budget %+v", field, q, b)
			}
		}
		if r == nil {
			return
		}
		if back := ParseRateLimit([]string{r.String()}); len(r) > 0 && !maps.Equal(back, r) {
			t.Fatalf("%q gave %v, which reads back as %v", field, r, back)
		}
	})
}
