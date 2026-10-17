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
	card := newToolCardForTest("probe", "")
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
	if !strings.HasPrefix(lines[1], ActiveTheme().GetBgAnsi("toolPendingBg")) {
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
	if !strings.HasPrefix(lines[1], ActiveTheme().GetBgAnsi("toolSuccessBg")) {
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
	card := newToolCardForTest("broken", "")
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

// packages/coding-agent/test/tool-execution-component.test.ts:478-506 supplies a definition with neither renderer. The definition fallback must show ten leading lines, not the separate generic-tool preview. Its call fallback shows the arguments (tool-execution.ts:155-157, formatToolCallWithArgs).
func TestDefinitionCardFallbackResultsUpstream(t *testing.T) {
	card := newToolCardForTest("custom_tool", "")
	card.SetDefinition(&ToolDefinitionRenderers{}, json.RawMessage(`{"foo":"bar"}`))
	output := make([]string, 15)
	for i := range output {
		output[i] = fmt.Sprintf("line-%d", i+1)
	}
	card.SetResult(strings.Join(output, "\n"), false, 0)

	// Upstream 0.99.1 with its default app keybindings renders a spacer, Box(1, 1), call, result, and bottom padding. Preserve every display row and padding cell.
	const width = 120
	padded := func(text string) string { return text + strings.Repeat(" ", width-len(text)) }
	for _, expanded := range []bool{false, true} {
		t.Run(fmt.Sprintf("expanded=%t", expanded), func(t *testing.T) {
			card.SetExpanded(expanded)
			want := []string{"", padded(""), padded(` custom_tool foo="bar"`)}
			if expanded {
				want = []string{"", padded(""), padded(" custom_tool"), padded("   foo: bar")}
			}
			display := output
			if !expanded {
				display = output[:10]
			}
			for _, line := range display {
				want = append(want, padded(" "+line))
			}
			if !expanded {
				want = append(want, padded(" ... (5 more lines, ctrl+o to expand)"))
			}
			want = append(want, padded(""))
			got := slices.Clone(card.Render(width))
			for i := range got {
				got[i] = widthx.StripAnsi(got[i])
			}
			if !slices.Equal(got, want) {
				t.Fatalf("fallback rows = %q, want %q", got, want)
			}
		})
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
	card := newToolCardForTest("self", "")
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
	card := newToolCardForTest("async", "")
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
