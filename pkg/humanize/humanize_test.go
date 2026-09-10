package humanize_test

import (
	"math"
	"testing"
	"time"

	"github.com/Geogboe/boxy/pkg/humanize"
)

func TestCommaInt(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{4096, "4,096"},
		{1234567, "1,234,567"},
		{-1234567, "-1,234,567"},
		{-999, "-999"},
		// Regression: negating math.MinInt64 overflows (no positive int64
		// can represent that magnitude), which previously produced a
		// doubled leading minus sign instead of a correctly formatted value.
		{math.MinInt64, "-9,223,372,036,854,775,808"},
		{math.MaxInt64, "9,223,372,036,854,775,807"},
	}
	for _, tc := range cases {
		if got := humanize.CommaInt(tc.in); got != tc.want {
			t.Errorf("CommaInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{42 * time.Second, "42s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{3 * time.Minute, "3m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{11*time.Hour + 30*time.Minute, "11h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{48 * time.Hour, "2d"},
		{-time.Second, "—"},
	}
	for _, tc := range cases {
		if got := humanize.ShortDuration(tc.in); got != tc.want {
			t.Errorf("ShortDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
