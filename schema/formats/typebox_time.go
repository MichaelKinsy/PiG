/*--------------------------------------------------------------------------

TypeBox

The MIT License (MIT)

Copyright (c) 2017-2026 Haydn Paterson

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.

---------------------------------------------------------------------------*/

// Ports packages/schema/src/formats/typebox/time.ts

package formats

import (
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var timeRE = lazyregexp.New(`^(\d\d):(\d\d):(\d\d)(?:\.\d+)?(?:([Zz])|([+-])(\d\d):(\d\d))?$`)

// IsTime returns true if the value is an ISO time string that carries a time zone.
// Specification: https://datatracker.ietf.org/doc/html/rfc3339.
func IsTime(value string) bool { return IsTimeWithZone(value, true) }

// IsTimeWithZone is Pi's IsTime with its optional strictTimeZone argument: when strictTimeZone is true the value must end in a time zone offset or "Z"/"z".
func IsTimeWithZone(value string, strictTimeZone bool) bool {
	matches := timeRE.FindStringSubmatch(value)
	if matches == nil {
		return false
	}
	if strictTimeZone && matches[4] == "" && matches[5] == "" {
		return false
	}
	hr := plusDigits(matches[1])
	minute := plusDigits(matches[2])
	sec := plusDigits(matches[3])
	if hr > 23 || minute > 59 || sec > 60 {
		return false
	}
	tzH := plusDigits(matches[6])
	tzM := plusDigits(matches[7])
	if matches[5] != "" && (tzH > 23 || tzM > 59) {
		return false
	}
	if sec < 60 {
		return true
	}
	// Leap second handling: must normalize to 23:59:60 UTC (1439 total UTC minutes)
	tzSign := 1
	if matches[5] == "-" {
		tzSign = -1
	}
	totalUtcMin := hr*60 + minute - tzSign*(tzH*60+tzM)
	return ((totalUtcMin%1440)+1440)%1440 == 1439
}
