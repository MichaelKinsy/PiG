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

// Ports packages/schema/src/formats/typebox/idn_email.ts

package formats

import (
	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Pi's literal has the i and u flags and a lookbehind (?<!-) that RE2 lacks. A label is a letter or digit, then up to 62 letters, digits or hyphens, and does not end in a hyphen: [\p{L}\p{N}](?:[\p{L}\p{N}-]{0,61}[\p{L}\p{N}])?. The u flag makes JavaScript fold case by Unicode simple case folding, as RE2's (?i) does, and `.` excludes the four line terminators.
//
//portlint:allow regexfold the u flag gives JavaScript Unicode simple case folding, which is what (?i) means in RE2
var idnEmailRE = lazyregexp.New(`(?i)^(?:[A-Za-z0-9!#$%&'*+/=?^_` + "`" + `{|}~\x{0080}-\x{10FFFF}-]+(?:\.[A-Za-z0-9!#$%&'*+/=?^_` + "`" + `{|}~\x{0080}-\x{10FFFF}-]+)*|"(?:[^"\\]|\\[^\n\r\x{2028}\x{2029}])*")@[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,61}[\p{L}\p{N}])?(?:\.[\p{L}\p{N}](?:[\p{L}\p{N}-]{0,61}[\p{L}\p{N}])?)*$`)

// IsIdnEmail returns true if the value is an IdnEmail.
// Specification: https://datatracker.ietf.org/doc/html/rfc4952.
func IsIdnEmail(value string) bool {
	return idnEmailRE.MatchString(norm.NFC.String(jsstring.Canonical(value)))
}
