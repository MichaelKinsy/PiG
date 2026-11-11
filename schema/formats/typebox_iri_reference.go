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

// Ports packages/schema/src/formats/typebox/iri_reference.ts

package formats

import (
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// hasInvalidIriReferenceChars is /[\x00-\x20\x7F\\]|%(?![0-9a-fA-F]{2})/: an unencoded space (0x20), backslash, control character (0x00-0x1F, 0x7F), or malformed percent-encoding.
func hasInvalidIriReferenceChars(value string) bool {
	for i := range len(value) {
		if value[i] <= 0x20 || value[i] == 0x7f || value[i] == '\\' {
			return true
		}
	}
	return hasInvalidPercentEncoding(value)
}

// hasMalformedScheme is /^[a-zA-Z][a-zA-Z0-9+\-.]*\/\//: a scheme-like prefix followed by "//" without a colon (e.g. "httpx//example.com").
func hasMalformedScheme(value string) bool {
	if len(value) == 0 || !isAsciiLetter(value[0]) {
		return false
	}
	i := 1
	for i < len(value) {
		c := value[i]
		if !isAsciiLetter(c) && (c < '0' || c > '9') && c != '+' && c != '-' && c != '.' {
			break
		}
		i++
	}
	return len(value) >= i+2 && value[i] == '/' && value[i+1] == '/'
}

func isAsciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// IsIriReference returns true if the value is an IRI reference.
// Specification: https://tools.ietf.org/html/rfc3987.
func IsIriReference(value string) bool {
	if hasInvalidIriReferenceChars(value) || hasMalformedScheme(value) {
		return false
	}
	// URL.canParse converts its argument to a USVString, so a lone surrogate reaches the parser as U+FFFD.
	_, err := nodeurl.ResolveURL(jsstring.StreamText(value), "http://example.com")
	return err == nil
}
