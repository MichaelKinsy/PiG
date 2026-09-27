package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(widthx.StripAnsi(line), " ")
	}
	return out
}

// Upstream's default shell is a Spacer(1) over Box(1, 1) that holds the call
// component and, once a result exists, the result component, in the pending
// background while partial and the success background after.
func TestDefinitionCardDefaultShell(t *testing.T) {
	var inputs []ToolRenderInput
	card := NewToolExecutionComponent("probe", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call: func(input ToolRenderInput) (Component, bool) {
			inputs = append(inputs, input)
			return NewPaddedText("CALL "+string(input.Args), 0, 0, nil), true
		},
		Result: func(input ToolRenderInput) (Component, bool) {
			return NewPaddedText(fmt.Sprintf("RESULT partial=%t", input.IsPartial), 0, 0, nil), true
		},
	}, json.RawMessage(`{"a":1}`))

	lines := card.Render(30)
	if got, want := plainLines(lines), []string{"", "", ` CALL {"a":1}`, ""}; !slices.Equal(got, want) {
		t.Fatalf("pending card = %q, want %q", got, want)
	}
	if !strings.HasPrefix(lines[1], ActiveTheme().Bg("toolPendingBg")) {
		t.Fatalf("pending card background = %q", lines[1])
	}

	card.SetStreaming("partial")
	if got, want := plainLines(card.Render(30)), []string{"", "", ` CALL {"a":1}`, " RESULT partial=true", ""}; !slices.Equal(got, want) {
		t.Fatalf("partial card = %q, want %q", got, want)
	}
	card.SetResult("done", false, time.Second)
	lines = card.Render(30)
	if got, want := plainLines(lines), []string{"", "", ` CALL {"a":1}`, " RESULT partial=false", ""}; !slices.Equal(got, want) {
		t.Fatalf("final card = %q, want %q", got, want)
	}
	if !strings.HasPrefix(lines[1], ActiveTheme().Bg("toolSuccessBg")) {
		t.Fatalf("final card background = %q", lines[1])
	}
	if last := inputs[len(inputs)-1]; last.IsPartial || last.Expanded {
		t.Fatalf("final renderer input = %+v, want complete and collapsed", last)
	}
}

// Upstream catches a renderer that throws and draws its fallbacks: the tool
// name in toolTitle, and the first ten output lines with an expand hint.
// A result never changes the expansion, not even an error.
func TestDefinitionCardFallbacksAndExpansion(t *testing.T) {
	card := NewToolExecutionComponent("broken", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call:   func(ToolRenderInput) (Component, bool) { return nil, false },
		Result: func(ToolRenderInput) (Component, bool) { return nil, false },
	}, nil)
	output := make([]string, 12)
	for i := range output {
		output[i] = fmt.Sprintf("line %d", i+1)
	}
	card.SetResult(strings.Join(output, "\n"), true, 0)
	if !card.Collapsed {
		t.Fatal("an error result expanded the card")
	}
	got := plainLines(card.Render(40))
	want := append([]string{"", "", " broken"}, prefixed(output[:10])...)
	want = append(want, " ... (2 more lines, ctrl+o to expand)", "")
	if !slices.Equal(got, want) {
		t.Fatalf("collapsed fallback = %q\nwant %q", got, want)
	}
	card.SetExpanded(true)
	got = plainLines(card.Render(40))
	want = append(append([]string{"", "", " broken"}, prefixed(output)...), "")
	if !slices.Equal(got, want) {
		t.Fatalf("expanded fallback = %q\nwant %q", got, want)
	}
}

func prefixed(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = " " + line
	}
	return out
}

// renderShell "self" draws the components after one blank line and nothing at
// all when they draw nothing.
func TestDefinitionCardSelfShell(t *testing.T) {
	card := NewToolExecutionComponent("self", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Self:   true,
		Call:   func(ToolRenderInput) (Component, bool) { return NewPaddedText("", 0, 0, nil), true },
		Result: func(ToolRenderInput) (Component, bool) { return NewPaddedText("self result", 0, 0, nil), true },
	}, nil)
	if got := card.Render(30); len(got) != 0 {
		t.Fatalf("empty self card = %q, want no rows", got)
	}
	card.SetResult("ok", false, 0)
	if got, want := plainLines(card.Render(30)), []string{"", "self result"}; !slices.Equal(got, want) {
		t.Fatalf("self card = %q, want %q", got, want)
	}
}

type asyncComponent struct {
	Text
	fallback func(int) []string
	dirty    bool
}

func (a *asyncComponent) SetRendererFallback(fallback func(int) []string) { a.fallback = fallback }
func (a *asyncComponent) IsDirty() bool                                   { return a.dirty }

// Every state change and invalidation reruns the renderers, as upstream's
// updateDisplay does; a component rendered by an extension process receives
// the card's fallback, and its new frame marks the card dirty.
func TestDefinitionCardRerunsRenderersOnInvalidate(t *testing.T) {
	calls := 0
	component := &asyncComponent{Text: *NewPaddedText("async", 0, 0, nil)}
	card := NewToolExecutionComponent("async", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Call: func(ToolRenderInput) (Component, bool) {
			calls++
			return component, true
		},
	}, nil)
	card.Render(30)
	card.NeedsRedraw()
	card.Render(30)
	if calls != 1 {
		t.Fatalf("renderer ran %d times without a state change, want 1", calls)
	}
	card.Invalidate()
	card.Render(30)
	if calls != 2 {
		t.Fatalf("renderer ran %d times after invalidate, want 2", calls)
	}
	if component.fallback == nil || !slices.Equal(plainLines(component.fallback(20)), []string{"async"}) {
		t.Fatal("the card did not hand its call fallback to the async component")
	}
	card.NeedsRedraw()
	if card.IsDirty() {
		t.Fatal("clean card reported dirty")
	}
	component.dirty = true
	if !card.IsDirty() {
		t.Fatal("a new async frame did not mark the card dirty")
	}
}
