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

// Ports packages/schema/src/formats/typebox/date.ts

package formats

import (
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var dateDays = [...]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
var dateRE = lazyregexp.New(`^(\d\d\d\d)-(\d\d)-(\d\d)$`)

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// IsDate returns true if the value is a ISO8601 Date component string.
// Specification: https://datatracker.ietf.org/doc/html/rfc3339. Example: `2020-12-12`.
func IsDate(value string) bool {
	matches := dateRE.FindStringSubmatch(value)
	if matches == nil {
		return false
	}
	year := plusDigits(matches[1])
	month := plusDigits(matches[2])
	day := plusDigits(matches[3])
	if month < 1 || month > 12 {
		return false
	}
	limit := dateDays[month]
	if month == 2 && isLeapYear(year) {
		limit = 29
	}
	return day >= 1 && day <= limit
}
