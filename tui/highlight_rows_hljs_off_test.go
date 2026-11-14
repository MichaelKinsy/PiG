//go:build pig_strip_syntax_highlight

package tui

// withHighlightedCode leaves out the rows that expect highlighted code (highlight_rows_hljs_on_test.go): this build links no
// highlight.js.
func withHighlightedCode[T any](...T) []T { return nil }

// warmHighlightKeepsState is what WarmHighlight documents for this build: without syntax highlighting it reports false and leaves
// no state (highlight_resume_hljs_off.go), which a test that asserts a resume state in a stock build asserts here.
func warmHighlightKeepsState() bool { return false }
