//go:build !pig_strip_mermaid

package codingagent

// The test of mermaid_interactive_test.go selects a theme, so TestThemeTestsRestoreTheProcessTheme runs it too; that file is not in a build that
// strips mermaid, and neither is this entry.
func init() {
	themeRestoringTests["TestInteractiveUserMessageRunsBuiltinMarkdownTransform"] = TestInteractiveUserMessageRunsBuiltinMarkdownTransform
}
