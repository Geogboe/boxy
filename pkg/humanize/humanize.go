// Package humanize converts raw machine values into human-presentable
// strings — comma-grouped integers today; the same category of helper as
// byte-size ("4.2 MB"), relative-time ("3 days ago"), or ordinal ("1st")
// formatting belongs here too as those needs come up (see dustin/go-humanize
// for the shape of a mature version of this idea). It has no boxy-specific
// coupling and is safe to use from any consumer, in-repo or not. Scope
// discipline: this package formats values for *display*; it does not parse
// human input back into machine values, and it is not a general string-
// utilities dumping ground — a helper that doesn't fit "raw value -> human
// string" belongs elsewhere.
package humanize

import (
	"strconv"
	"strings"
)

// CommaInt formats value with thousands separators, e.g. 4096 -> "4,096" and
// -1234567 -> "-1,234,567".
func CommaInt(value int64) string {
	digits := strconv.FormatInt(value, 10)
	// Strip the sign from strconv's output rather than negating value: -value
	// overflows for math.MinInt64 (no positive int64 can represent that
	// magnitude), which previously produced a doubled leading minus sign.
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = digits[1:]
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	return b.String()
}
