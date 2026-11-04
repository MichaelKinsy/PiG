package tui

import "sync/atomic"

// chalkModifiersOff is set when upstream's chalk would draw no modifiers in
// this process. Upstream's theme.bold, italic, underline, inverse and
// strikethrough are chalk calls, which return their text unchanged when
// chalk's stdout color level is 0 (CI=1, TERM=dumb, stdout not a terminal),
// while theme.fg writes its escape sequences directly and still colors.
var chalkModifiersOff atomic.Bool

// SetChalkModifiers records whether chalk's stdout color level is above zero.
// The entry point sets it once from the process environment; it defaults to
// enabled.
func SetChalkModifiers(enabled bool) { chalkModifiersOff.Store(!enabled) }

// ChalkBold, ChalkItalic, ChalkUnderline, ChalkInverse and ChalkStrikethrough are
// upstream theme.bold, italic, underline, inverse and strikethrough: chalk
// calls that draw nothing when chalk's color level is 0.
func ChalkBold(text string) string { return markdownDecoration("\x1b[1m", SGRBoldDimReset, text) }

func ChalkItalic(text string) string { return markdownDecoration("\x1b[3m", SGRItalicReset, text) }

func ChalkUnderline(text string) string {
	return markdownDecoration("\x1b[4m", SGRUnderlineReset, text)
}

func ChalkInverse(text string) string { return markdownDecoration("\x1b[7m", SGRInverseReset, text) }

func ChalkStrikethrough(text string) string {
	return markdownDecoration("\x1b[9m", SGRStrikeReset, text)
}
