//go:build pig_strip_syntax_highlight

package tui

// withHighlightedCode leaves out the rows that expect highlighted code (highlight_rows_hljs_on_test.go): this build links no
// highlight.js.
func withHighlightedCode[T any](...T) []T { return nil }
