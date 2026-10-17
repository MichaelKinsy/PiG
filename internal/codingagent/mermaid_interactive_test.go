//go:build !pig_strip_mermaid

package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pig additive (D92): the interactive Mermaid transform cases, which need the diagram renderer a pig_strip_mermaid Binary compiles out.

func TestAssistantThinkingUsesThinkingTransformContext(t *testing.T) {
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg(""))
	block := m.newAssistantMessageBlock()
	// Pi emits a final partial-parse warning for this flowchart, but never transforms the thinking block.
	const diagram = "```mermaid\nflowchart LR\n  A[Foo] invalid\n```"
	block.SetContent([]tui.AssistantSegment{{Thinking: true, Text: diagram}, {Text: diagram}})
	got := stripANSITest(strings.Join(block.Render(80), "\n"))
	if strings.Count(got, "Mermaid diagram not rendered") != 1 || !strings.Contains(got, "flowchart LR") {
		t.Fatalf("thinking must retain the code block while only text gets the Mermaid transform: %q", got)
	}
}

// Pi interactive-mode.ts:2137-2138,3801-3814 passes the built-in transformer to user messages as well as assistant messages.
func TestInteractiveUserMessageRunsBuiltinMarkdownTransform(t *testing.T) {
	previousTheme, previousCaps := tui.ActiveTheme().Name, tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps); tui.SetThemeByName(previousTheme) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	m := &InteractiveMode{outputPad: 1}
	block := m.newUserMessageBlock("```mermaid\nflowchart LR\nA --> B\n```")
	plain := widthx.StripAnsi(strings.Join(block.Render(80), "\n"))
	if strings.Contains(plain, "```mermaid") || !strings.Contains(plain, "┌") || !strings.Contains(plain, "A") || !strings.Contains(plain, "B") {
		t.Fatalf("user Mermaid transform missing: %q", plain)
	}
	data, err := json.Marshal(struct {
		Name  string   `json:"name"`
		Lines []string `json:"lines"`
	}{"user-builtin-transform", block.Render(80)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("USER_BUILTIN_TRANSFORM:%s", data)
}
