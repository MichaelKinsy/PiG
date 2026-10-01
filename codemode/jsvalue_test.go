package codemode_test

import "testing"

// Enum and const values render with JSON.stringify, which writes U+FFFD, U+2028 and HTML characters as they are and
// escapes only quotes, backslashes and control characters (measured with Node: JSON.stringify("a\ufffd") is "a�").
func TestEnumValuesRenderLikeJSONStringify(t *testing.T) {
	check(t, typeOf(`{"enum":["a\ufffd","\u2028<>&","q\"\\\u0001"]}`), `"a`+"\ufffd"+`" | "`+"\u2028"+`<>&" | "q\"\\\u0001"`)
}
