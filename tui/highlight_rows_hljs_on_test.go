//go:build !pig_strip_syntax_highlight

package tui

// withHighlightedCode marks the rows of a test table that expect highlighted code: a code block of a language highlight.js
// highlights, drawn under a stock theme, which Pi's rows hold highlighted. A build that strips syntax-highlight draws such a
// block as one of an unknown language (highlight_hljs_off.go) and leaves these rows out (highlight_rows_hljs_off_test.go).
func withHighlightedCode[T any](rows ...T) []T { return rows }

// warmHighlightKeepsState is what WarmHighlight documents for this build: it leaves a resume state for code highlight.js resumes
// over and reports it. A test that asserts a resume state asserts it where this is true, and that no state is reported or left
// where it is false (highlight_rows_hljs_off_test.go).
func warmHighlightKeepsState() bool { return true }
