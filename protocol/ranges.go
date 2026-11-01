package protocol

import (
	"strconv"
	"strings"
)

// A fetch that was cut resumes by range (PROTOCOL.md §11.12; RFC 9110 §14): it
// asks for the bytes from N on, with Range: bytes=N- (RangeAsk), and is
// answered 206 with the bytes from N to the end and a Content-Range that says
// so (ContentRange), or 200 with the whole body.

// RangeAsk is the Range a fetch sends to resume at byte from: one open-ended
// byte range, "bytes=<from>-" (RFC 9110 §14.1.2).
func RangeAsk(from int64) string { return "bytes=" + strconv.FormatInt(from, 10) + "-" }

// RangeAsked reads a fetch's Range and If-Range fields as a server answers
// them (PROTOCOL.md §11.12): from is the first byte of the one open-ended byte
// range the request asks for, "bytes=N-" with N in digits, the unit in any
// ASCII case (RFC 9110 §14.1; LowerASCII), and ok is false for anything else, which a server
// answers with the whole body as RFC 9110 §14.2 lets it ignore a Range: no
// Range, two Range lines, several ranges, a closed range, a suffix range, a
// unit other than bytes, or a Range beside an If-Range, whose condition is
// false where nothing carries a validator, as no fetch does (§13.1.5). A
// number with more digits than an int64 holds asks from past any length.
// Whether from is below the ciphertext's length, as a range a server answers
// 206 must be, is the caller's to compare: it alone knows the length.
func RangeAsked(ranges, ifRange []string) (from int64, ok bool) {
	if len(ranges) != 1 || len(ifRange) > 0 {
		return 0, false
	}
	unit, spec, cut := strings.Cut(ranges[0], "=")
	n, open := strings.CutSuffix(spec, "-")
	if !cut || !open || LowerASCII(unit) != "bytes" || n == "" || strings.Trim(n, "0123456789") != "" {
		return 0, false
	}
	from, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		return int64(^uint64(0) >> 1), true // more digits than any length
	}
	return from, true
}

// ContentRange is the Content-Range of a fetch's 206 that answers the bytes
// from byte from of a ciphertext of length bytes: "bytes <from>-<last>/<length>",
// each number in digits with no leading zero, the one form a server writes
// (PROTOCOL.md §11.12).
func ContentRange(from, length int64) string {
	return "bytes " + strconv.FormatInt(from, 10) + "-" + strconv.FormatInt(length-1, 10) + "/" + strconv.FormatInt(length, 10)
}

// IsContentRange reports whether a 206's Content-Range lines are one line
// answering the bytes from byte from of a ciphertext of length bytes, as
// ContentRange writes it but with the unit in any ASCII case, as a reader
// takes it (RFC 9110 §14.1; PROTOCOL.md §11.12).
func IsContentRange(lines []string, from, length int64) bool {
	if len(lines) != 1 {
		return false
	}
	unit, rest, _ := strings.Cut(lines[0], " ")
	_, want, _ := strings.Cut(ContentRange(from, length), " ")
	return LowerASCII(unit) == "bytes" && rest == want
}
