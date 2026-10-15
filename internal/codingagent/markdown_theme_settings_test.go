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
			settings.Markdown = &MarkdownSettings{CodeBlockIndent: &tc.indent}
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

// getMarkdownThemeWithSettings builds a theme per block in Pi; here blocks created under one code-block indent share one theme,
// and a changed setting reaches the next block: an old block keeps the indent it was created with, as in Pi.
func TestMarkdownThemeIsSharedUntilTheCodeBlockIndentChanges(t *testing.T) {
	manager := &SettingsManager{merged: Settings{Markdown: &MarkdownSettings{CodeBlockIndent: new("  ")}}}
	m := &InteractiveMode{opts: InteractiveModeOptions{SettingsManager: manager}}
	first := m.markdownThemeWithSettings()
	if got := *first.CodeBlockIndent; got != "  " {
		t.Fatalf("indent %q, want two spaces", got)
	}
	if m.markdownThemeWithSettings() != first {
		t.Fatal("an unchanged setting built a second theme")
	}
	manager.merged.Markdown = &MarkdownSettings{CodeBlockIndent: new("\t")}
	second := m.markdownThemeWithSettings()
	if second == first || *second.CodeBlockIndent != "\t" {
		t.Fatalf("a changed setting did not reach the next theme: %q", *second.CodeBlockIndent)
	}
	if *first.CodeBlockIndent != "  " {
		t.Fatalf("the first block's theme changed to %q", *first.CodeBlockIndent)
	}
}
