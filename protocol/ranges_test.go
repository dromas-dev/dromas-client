package protocol

import (
	"math"
	"strings"
	"testing"
)

// A fetch's Range is read as one open-ended byte range, "bytes=N-", the unit
// in any ASCII case (RFC 9110 §14.1), and nothing else: several ranges, a closed
// one, a suffix, another unit, a form with spaces, two lines, or a Range
// beside an If-Range are answered with the whole body (PROTOCOL.md §11.12).
func TestRangeAsked(t *testing.T) {
	for _, c := range []struct {
		ranges, ifRange []string
		from            int64
		ok              bool
	}{
		{[]string{"bytes=5-"}, nil, 5, true},
		{[]string{"bytes=0-"}, nil, 0, true},
		{[]string{"Bytes=5-"}, nil, 5, true},
		{[]string{"BYTES=007-"}, nil, 7, true},
		{[]string{RangeAsk(16777215)}, nil, 16777215, true},
		{[]string{"bytes=" + strings.Repeat("9", 30) + "-"}, nil, math.MaxInt64, true},
		{nil, nil, 0, false},
		{[]string{""}, nil, 0, false},
		{[]string{"bytes=5-"}, []string{`"tag"`}, 0, false},
		{[]string{"bytes=5-"}, []string{""}, 0, false},
		{[]string{"bytes=5-", "bytes=6-"}, nil, 0, false},
		{[]string{"bytes=5-9"}, nil, 0, false},
		{[]string{"bytes=-5"}, nil, 0, false},
		{[]string{"bytes=-"}, nil, 0, false},
		{[]string{"bytes=5-,7-"}, nil, 0, false},
		{[]string{"bytes=5-, 7-"}, nil, 0, false},
		{[]string{"bytes= 5-"}, nil, 0, false},
		{[]string{"bytes=5- "}, nil, 0, false},
		{[]string{"bytes =5-"}, nil, 0, false},
		{[]string{"bytes=+5-"}, nil, 0, false},
		{[]string{"bytes=0x5-"}, nil, 0, false},
		{[]string{"bytes=５-"}, nil, 0, false},
		{[]string{"items=5-"}, nil, 0, false},
		{[]string{"byteſ=5-"}, nil, 0, false}, // a token is ASCII: U+017F folds to s only by Unicode's rules (#405)
		{[]string{"BYTEſ=5-"}, nil, 0, false},
		{[]string{"bytes5-"}, nil, 0, false},
		{[]string{"bytes=5"}, nil, 0, false},
	} {
		from, ok := RangeAsked(c.ranges, c.ifRange)
		if from != c.from || ok != c.ok {
			t.Errorf("Range %q, If-Range %q: %d, %v; want %d, %v", c.ranges, c.ifRange, from, ok, c.from, c.ok)
		}
	}
}

// A 206's Content-Range is written in one form, RFC 9110 §14.4's, each number
// in digits with no leading zero, and a reader takes it with its unit in any
// ASCII case (RFC 9110 §14.1).
func TestContentRange(t *testing.T) {
	for _, c := range []struct {
		from, length int64
		want         string
	}{
		{5, 10, "bytes 5-9/10"},
		{0, 1, "bytes 0-0/1"},
		{1 << 20, MaxCiphertext, "bytes 1048576-16777215/16777216"},
	} {
		if got := ContentRange(c.from, c.length); got != c.want {
			t.Errorf("ContentRange(%d, %d) = %q, want %q", c.from, c.length, got, c.want)
		}
	}
	for _, c := range []struct {
		lines []string
		ok    bool
	}{
		{[]string{"bytes 5-9/10"}, true},
		{[]string{"BYTES 5-9/10"}, true},
		{[]string{"Bytes 5-9/10"}, true},
		{[]string{"bytes 5-9/10", "bytes 5-9/10"}, false},
		{nil, false},
		{[]string{"bytes 4-9/10"}, false},
		{[]string{"bytes 5-9/11"}, false},
		{[]string{"bytes 05-9/10"}, false},
		{[]string{"bytes  5-9/10"}, false},
		{[]string{"byte 5-9/10"}, false},
		{[]string{"bytes 5-9/*"}, false},
		{[]string{"bytes */10"}, false},
	} {
		if got := IsContentRange(c.lines, 5, 10); got != c.ok {
			t.Errorf("IsContentRange(%q, 5, 10) = %v", c.lines, got)
		}
	}
	if got := RangeAsk(42); got != "bytes=42-" {
		t.Errorf("RangeAsk(42) = %q", got)
	}
}
