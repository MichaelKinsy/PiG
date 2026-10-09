package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:1378-1383 getMarkdownThemeWithSettings: the markdown.codeBlockIndent setting reaches every chat Markdown
// built with it (:3512 assistant, :3895-3973 user, summary and custom messages, :6790 changelog, :6921 hotkeys).
func TestMarkdownCodeBlockIndentSettingReachesChatComponents(t *testing.T) {
	indentOf := func(component tui.Component) string {
		for _, line := range component.Render(60) {
			plain := stripANSI(line)
			if trimmed := strings.TrimLeft(plain, " \t"); strings.HasPrefix(trimmed, "CODE") {
				return plain[:len(plain)-len(trimmed)]
			}
		}
		var rows []string
		for _, line := range component.Render(60) {
			rows = append(rows, stripANSI(line))
		}
		t.Fatalf("no CODE row in %q", rows)
		return ""
	}
	const body = "```\nCODE\n```"
	for _, tc := range []struct {
		indent string
		want   string // leading columns: the component's one column of padding plus the code block indent
	}{{"", "   "}, {"    ", "     "}, {"\t", " \t"}} {
		settings := Settings{}
		if tc.indent != "" {
			settings.Markdown = &MarkdownSettings{CodeBlockIndent: tc.indent}
		}
		m := &InteractiveMode{
			chatContainer: tui.NewContainer(),
			tuiInst:       tui.NewWithOutput(io.Discard, 80, 24),
			outputPad:     1,
			opts:          InteractiveModeOptions{SettingsManager: &SettingsManager{merged: settings}},
		}
		assistant := m.newAssistantMessageBlock()
		assistant.SetTextDelta(body)
		for name, component := range map[string]tui.Component{
			"user":       m.newUserMessageBlock(body),
			"assistant":  assistant,
			"compaction": tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: body}, m.markdownThemeWithSettings(), 1),
		} {
			if c, ok := component.(interface{ SetExpanded(bool) }); ok {
				c.SetExpanded(true)
			}
			if got := indentOf(component); got != tc.want {
				t.Errorf("%s with codeBlockIndent %q: leading %q, want %q", name, tc.indent, got, tc.want)
			}
		}
	}
}
