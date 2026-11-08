package terminals

import (
	"errors"
	"time"

	"github.com/dlclark/regexp2"
)

// JSRegExp is a JavaScript regular expression (`new RegExp(pattern, "m" | "mi")`) over text: ECMAScript semantics of
// regexp2, multiline.
type JSRegExp struct{ re *regexp2.Regexp }

// CompileJSRegExp compiles pattern as `new RegExp(pattern, flags)` does: "m", plus "i" with ignoreCase. The error reads
// as V8's SyntaxError message.
func CompileJSRegExp(pattern string, ignoreCase bool) (*JSRegExp, error) {
	options := regexp2.RegexOptions(regexp2.ECMAScript | regexp2.Multiline)
	if ignoreCase {
		options |= regexp2.IgnoreCase
	}
	re, err := regexp2.Compile(pattern, options)
	if err != nil {
		return nil, errors.New("Invalid regular expression: /" + pattern + "/: " + err.Error())
	}
	re.MatchTimeout = 5 * time.Second
	return &JSRegExp{re: re}, nil
}

// Test is `pattern.test(text)`: a match anywhere.
func (re *JSRegExp) Test(text string) bool {
	matched, err := re.re.MatchString(text)
	return err == nil && matched
}
