package humanize_test

import (
	"math"
	"testing"

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
