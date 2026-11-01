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

// Ports packages/schema/src/formats/typebox/iri.ts

package formats

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

const ipvFutureMatchMaxLength = 2048

var ipvFutureMatch = lazyregexp.New(`\[[vV][0-9a-fA-F]+\.[^\]]+\]`) // Guarded By ipvFutureMatchMaxLength

// hasInvalidIriChars is /[\x00-\x20<>^`{|}\\]/.
func hasInvalidIriChars(value string) bool {
	for i := range len(value) {
		if value[i] <= 0x20 || strings.IndexByte("<>^`{|}\\", value[i]) >= 0 {
			return true
		}
	}
	return false
}

// hasInvalidPercentEncoding is /%(?![0-9a-fA-F]{2})/: a "%" not followed by two hexadecimal digits.
func hasInvalidPercentEncoding(value string) bool {
	for i := range len(value) {
		if value[i] == '%' && (i+2 >= len(value) || !isHexDigit(value[i+1]) || !isHexDigit(value[i+2])) {
			return true
		}
	}
	return false
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// narrowIpvFuture substitutes an IPvFuture address with a standard IPv6 loopback address ([::1]), because the WHATWG URL parser rejects IPvFuture literals, which are valid in RFC 3987. Only inputs under a length threshold are searched.
func narrowIpvFuture(value string) string {
	if jsstring.Length(value) < ipvFutureMatchMaxLength {
		if loc := ipvFutureMatch.FindStringIndex(value); loc != nil {
			return value[:loc[0]] + "[::1]" + value[loc[1]:]
		}
	}
	return value
}

// IsIri returns true if the value is a valid Internationalized Resource Identifier.
// Specification: https://datatracker.ietf.org/doc/html/rfc3987.
func IsIri(value string) bool {
	// 1. Reject strings containing unencoded whitespace or illegal control characters.
	if hasInvalidIriChars(value) {
		return false
	}
	// 2. Reject malformed percent-encoding triplets.
	if hasInvalidPercentEncoding(value) {
		return false
	}
	// 3. Delegate to the URL parser, patching the IPvFuture edge case beforehand. URL.canParse converts its argument to a USVString, so a lone surrogate reaches the parser as U+FFFD.
	_, err := nodeurl.ParseURL(jsstring.StreamText(narrowIpvFuture(value)))
	return err == nil
}
