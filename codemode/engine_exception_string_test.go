package codemode

import "testing"

// ECMAScript Error.prototype.toString (ES2024 20.5.3.4): the string is `name: message`, and the name alone when the message is empty.
// jsException.String is the host's rendering of a guest exception and follows it for the shapes the engine produces (a name with and without a
// message; the engine always supplies a name).
// Spec: https://tc39.es/ecma262/#sec-error.prototype.tostring
func TestJSExceptionStringFollowsErrorPrototypeToString(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{"TypeError", "x is not a function", "TypeError: x is not a function"},
		{"Error", "", "Error"},
		{"RangeError", "", "RangeError"},
	} {
		if got := (jsException{name: tc.name, message: tc.message}).String(); got != tc.want {
			t.Errorf("jsException{%q, %q}.String() = %q, want %q", tc.name, tc.message, got, tc.want)
		}
	}
}
