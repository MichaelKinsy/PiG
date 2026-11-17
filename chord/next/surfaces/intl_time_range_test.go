package surfaces

import (
	"testing"
	"time"
)

// inDateRange is TimeClip's range: the ends (±8.64e15 ms) are valid, one millisecond beyond is not. A time.Time carries
// digits below the millisecond that a Date does not; they truncate toward zero as TimeClip's ToIntegerOrInfinity does,
// so an instant less than one millisecond beyond an end is the end itself.
func TestInDateRangeAtTheEndsOfTheDateRange(t *testing.T) {
	const limit = 8_640_000_000_000_000 // ms
	at := func(ms int64, ns int64) time.Time {
		// ms is the floor millisecond; ns the nanoseconds past it.
		return time.UnixMilli(ms).UTC().Add(time.Duration(ns))
	}
	for _, c := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"lower end", at(-limit, 0), true},
		{"lower end, one nanosecond beyond", at(-limit-1, 999_999), true},
		{"lower end, half a millisecond beyond", at(-limit-1, 500_000), true},
		{"lower end, just over one millisecond beyond", at(-limit-1, 1), true},
		{"lower end, exactly one millisecond beyond", at(-limit-1, 0), false},
		{"lower end, two milliseconds beyond", at(-limit-2, 0), false},
		{"lower end, one millisecond inside", at(-limit+1, 0), true},
		{"upper end", at(limit, 0), true},
		{"upper end, one nanosecond beyond", at(limit, 1), true},
		{"upper end, just under one millisecond beyond", at(limit, 999_999), true},
		{"upper end, one millisecond beyond", at(limit+1, 0), false},
		{"upper end, one millisecond inside", at(limit-1, 0), true},
		{"the epoch", at(0, 0), true},
		{"just before the epoch", at(-1, 999_999), true},
	} {
		if got := inDateRange(c.at); got != c.want {
			t.Errorf("%s: inDateRange = %v, want %v", c.name, got, c.want)
		}
	}
}
