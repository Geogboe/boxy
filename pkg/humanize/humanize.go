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
	"fmt"
	"strconv"
	"strings"
	"time"
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

// ShortDuration formats a non-negative duration as a compact single-unit
// approximation — "42s", "3m", "11m", "1h", "2d" — the style used for a
// dashboard's "how long has this been in its current state" column rather
// than Go's own more precise but noisier "1h2m3s". A negative duration
// (e.g. a zero/unset timestamp subtracted from now producing a negative
// elapsed value) returns "—" rather than a misleading negative-looking
// string; callers with a genuinely unknown timestamp should check for that
// before calling and pass a zero duration is not assumed to mean "unknown"
// here, since a real zero-elapsed duration ("0s", just happened) is a valid
// input distinct from "unknown" — check the source timestamp instead.
func ShortDuration(d time.Duration) string {
	if d < 0 {
		return "—"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
