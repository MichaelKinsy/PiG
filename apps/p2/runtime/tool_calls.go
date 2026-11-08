package runtime

import "regexp"

// Ports apps/p2/src/runtime/tool-calls.ts: what every facet may read of a tool call's outcome as Durable records it.

// interruptedError is Durable's error for a call a restart cut short (an unsafe tool is not run again): interrupted,
// not failed. `\S` is JavaScript's: any character that is not white space.
var interruptedError = regexp.MustCompile(`^Tool [^\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+ was interrupted and may have partially run`)

// IsInterruptedError reports whether a call's error says a restart cut it short: it was interrupted, not failed. A
// value that is not a string is not.
func IsInterruptedError(err any) bool {
	text, ok := err.(string)
	return ok && interruptedError.MatchString(text)
}
