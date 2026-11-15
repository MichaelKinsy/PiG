//go:build !pig_strip_export_html

package codingagent

// The tests of mcp_tool_renderers_10285_export_test.go select a theme, so TestThemeTestsRestoreTheProcessTheme runs them too; that file is not
// in a build that strips export-html, and neither are these entries.
func init() {
	themeRestoringTests["TestRegression10285RendersThemInHTMLExportsToo"] = TestRegression10285RendersThemInHTMLExportsToo
	themeRestoringTests["TestRegression10285SlashExportRendersThemInHTMLExports"] = TestRegression10285SlashExportRendersThemInHTMLExports
}
