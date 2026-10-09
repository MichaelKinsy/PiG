package tui

// pi: packages/coding-agent/src/modes/interactive/components/custom-message.ts

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi custom-message.ts:23-45: the constructor builds Spacer(1) plus either the custom renderer's component or the default box.
func TestCustomMessageComponentChildren(t *testing.T) {
	message := &CustomMessage{CustomType: "notice", Content: "body"}
	def := NewCustomMessageComponent(message, nil, nil, 1)
	children := def.Children()
	if len(children) != 2 {
		t.Fatalf("default children = %d, want Spacer + Box", len(children))
	}
	if _, ok := children[0].(*Spacer); !ok {
		t.Fatalf("first child = %T, want *Spacer", children[0])
	}
	if _, ok := children[1].(*Box); !ok {
		t.Fatalf("second child = %T, want *Box", children[1])
	}

	custom := NewText("styled")
	withRenderer := NewCustomMessageComponent(message, func(*CustomMessage, MessageRenderOptions) Component { return custom }, nil, 1)
	children = withRenderer.Children()
	if len(children) != 2 || children[1] != Component(custom) {
		t.Fatalf("custom children = %v, want Spacer + the renderer's component", children)
	}
}

// Pi custom-message.ts:41-56,68-83: SetExpanded and SetOutputPad rebuild only on a change, Invalidate always rebuilds, and the
// renderer receives the current options.
func TestCustomMessageComponentRebuildsWithOptions(t *testing.T) {
	var seen []MessageRenderOptions
	c := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: "y"}, func(_ *CustomMessage, options MessageRenderOptions) Component {
		seen = append(seen, options)
		return NewText("custom")
	}, nil, 1)
	c.SetExpanded(false)
	c.SetOutputPad(1)
	c.SetExpanded(true)
	c.SetOutputPad(0)
	c.Invalidate()
	want := []MessageRenderOptions{{false, 1}, {true, 1}, {true, 0}, {true, 0}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("renderer options = %v, want %v", seen, want)
	}
}

// Pi custom-message.ts:78-83: a renderer that returns nothing or throws falls through to the default box.
func TestCustomMessageComponentFallsBackToDefault(t *testing.T) {
	message := &CustomMessage{CustomType: "notice", Content: "body text"}
	for name, renderer := range map[string]MessageRenderer{
		"nil result": func(*CustomMessage, MessageRenderOptions) Component { return nil },
		"panic":      func(*CustomMessage, MessageRenderOptions) Component { panic("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			c := NewCustomMessageComponent(message, renderer, nil, 1)
			text := widthx.StripAnsi(strings.Join(c.Render(40), "\n"))
			if !strings.Contains(text, "[notice]") || !strings.Contains(text, "body text") {
				t.Fatalf("default rendering missing: %q", text)
			}
		})
	}
}

// Pi custom-message.ts:97-101: the default body is Markdown rendered with the supplied markdown theme.
func TestCustomMessageComponentUsesMarkdownTheme(t *testing.T) {
	identity := func(s string) string { return s }
	theme := GetMarkdownTheme()
	theme.Bold = func(s string) string { return "<b>" + s + "</b>" }
	theme.Heading, theme.Italic, theme.Strikethrough, theme.Underline = identity, identity, identity, identity
	c := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: "**strong**"}, nil, &theme, 1)
	if got := strings.Join(c.Render(40), "\n"); !strings.Contains(got, "<b>") || !strings.Contains(got, "</b>") {
		t.Fatalf("markdown theme not applied: %q", got)
	}
}
