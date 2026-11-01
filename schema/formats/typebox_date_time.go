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

// Ports packages/schema/src/formats/typebox/date_time.ts

package formats

import "strings"

// IsDateTime returns true if the value is a ISO8601 DateTime string.
// Specification: https://datatracker.ietf.org/doc/html/rfc3339. Example: `2020-12-12T20:20:40+00:00`.
func IsDateTime(value string) bool {
	// value.split(/T/i) has exactly two parts when the value holds one "T" or "t".
	at := strings.IndexAny(value, "Tt")
	if at < 0 || strings.ContainsAny(value[at+1:], "Tt") {
		return false
	}
	return IsDate(value[:at]) && IsTime(value[at+1:])
}
