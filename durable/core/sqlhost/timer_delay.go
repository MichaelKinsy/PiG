// SPDX-License-Identifier: MIT

package sqlhost

import (
	"math"
	"time"
)

// timerDelay is the time from now until the core's timer deadline at, both epoch milliseconds. A deadline that is due, or not a
// number, is due at once; one beyond what a Duration holds waits as long as a Duration can.
func timerDelay(at, now float64) time.Duration {
	ms := at - now
	if !(ms > 0) {
		return 0
	}
	if ms >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms * float64(time.Millisecond))
}
