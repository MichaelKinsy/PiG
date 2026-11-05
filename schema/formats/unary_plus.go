// SPDX-License-Identifier: MIT

package formats

import "strconv"

// plusDigits is JavaScript's unary `+` on a regex capture group of `\d\d`/`\d\d\d\d` digits (date.ts IsDate, time.ts IsTime). The capture holds only ASCII digits, so Atoi cannot fail; an empty capture (a group that did not participate) is `+undefined || 0` in Pi's leap-second path and 0 here.
func plusDigits(capture string) int {
	//portlint:allow numbers the capture is ASCII digits only (date.ts DATE, time.ts TIME `\d` is ASCII in JS and RE2), where Atoi equals `+capture`; an unmatched group is `+(matches[6] || 0)` in time.ts IsTime
	n, _ := strconv.Atoi(capture)
	return n
}
