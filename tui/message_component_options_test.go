package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// upstream: user-message.ts:19-31 (constructor(text, markdownTheme = getMarkdownTheme(), outputPad = 1, markdownTransformers = [])) and rebuild() 38-59, which hand the three to the Markdown child. Each injected value reaches the rendered card, and the default stays the active theme, pad 1 and no transform.
func TestUserMessageComponentTakesTheConstructorInjections(t *testing.T) {
	render := func(block *UserMessageComponent) string {
		return widthx.StripAnsi(strings.Join(block.Render(30), "\n"))
	}
	theme := GetMarkdownTheme()
	theme.Bold = func(text string) string { return "<<" + text + ">>" }
	if got := render(NewUserMessageComponent("a **b** c", &theme, 1, nil)); !strings.Contains(got, "<<b>>") {
		t.Fatalf("the injected markdown theme must style bold:\n%s", got)
	}
	if got := render(NewUserMessageComponent("a **b** c", nil, 1, nil)); strings.Contains(got, "<<") {
		t.Fatalf("the default theme must not be the injected one:\n%s", got)
	}
	lead := func(block *UserMessageComponent) int {
		lines := strings.Split(render(block), "\n")
		line := lines[1] // row 0 is the top padding row
		return len(line) - len(strings.TrimLeft(line, " "))
	}
	if padded, flush := lead(NewUserMessageComponent("hi", nil, 1, nil)), lead(NewUserMessageComponent("hi", nil, 0, nil)); padded != 1 || flush != 0 {
		t.Fatalf("output pad: default %d, injected 0 gives %d; want 1 and 0", padded, flush)
	}
	transformed := NewUserMessageComponent("plain", nil, 1, []markdowntransform.MarkdownTransformer{func(text string, _ markdowntransform.MarkdownTransformContext) string { return strings.ToUpper(text) }})
	if got := render(transformed); !strings.Contains(got, "PLAIN") {
		t.Fatalf("the injected transform must rewrite the displayed text:\n%s", got)
	}
}

// upstream: assistant-message.ts:26-47 (constructor(message?, hideThinkingBlock = false, markdownTheme = getMarkdownTheme(), hiddenThinkingLabel = "Thinking...", outputPad = 1, markdownTransformers = [])) and updateContent() 100-200, which pass the theme to every Markdown block and the label and pad to the hidden-run and error rows.
func TestAssistantMessageComponentTakesTheConstructorInjections(t *testing.T) {
	render := func(block *AssistantMessageComponent) string {
		return widthx.StripAnsi(strings.Join(block.Render(40), "\n"))
	}
	theme := GetMarkdownTheme()
	theme.Bold = func(text string) string { return "<<" + text + ">>" }
	content := []AssistantSegment{{Thinking: true, Text: "SECRET"}, {Text: "an **answer**"}}

	themed := NewAssistantMessageComponent(nil, false, &theme, "", nil, nil)
	themed.SetContent(content)
	if got := render(themed); !strings.Contains(got, "<<answer>>") {
		t.Fatalf("the injected markdown theme must style text blocks:\n%s", got)
	}
	plain := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	plain.SetContent(content)
	if got := render(plain); strings.Contains(got, "<<") {
		t.Fatalf("the default theme must not be the injected one:\n%s", got)
	}

	hidden := NewAssistantMessageComponent(nil, true, nil, "Mulling it over", nil, nil)
	hidden.SetContent(content)
	if got := render(hidden); !strings.Contains(got, "Mulling it over") || strings.Contains(got, "Thinking...") || strings.Contains(got, "SECRET") {
		t.Fatalf("the injected hidden label must stand in for the run:\n%s", got)
	}

	lead := func(block *AssistantMessageComponent) int {
		block.SetContent([]AssistantSegment{{Text: "hi"}})
		for line := range strings.SplitSeq(render(block), "\n") {
			if strings.Contains(line, "hi") {
				return len(line) - len(strings.TrimLeft(line, " "))
			}
		}
		return -1
	}
	if padded, flush := lead(NewAssistantMessageComponent(nil, false, nil, "", nil, nil)), lead(NewAssistantMessageComponent(nil, false, nil, "", new(0), nil)); padded != 1 || flush != 0 {
		t.Fatalf("output pad: default %d, injected 0 gives %d; want 1 and 0", padded, flush)
	}
}
