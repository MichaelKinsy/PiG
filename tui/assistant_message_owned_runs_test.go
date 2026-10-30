package tui

// pig additive (D214): an extension that owns thinking runs leaves no spacing around a run that draws no rows.
// pi: packages/coding-agent/src/modes/interactive/components/assistant-message.ts (updateContent)

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// ownedRows renders block at width and returns its rows without escapes or trailing spaces, keeping blank rows.
func ownedRows(block *AssistantMessageComponent, width int) []string {
	var out []string
	for _, line := range block.Render(width) {
		out = append(out, strings.TrimRight(assistantLabelEscapes.ReplaceAllString(line, ""), " "))
	}
	return out
}

func emptyThinkingTransform(string, int) string { return "" }

// ownedBlock builds a block over content; owned declares the extension's ownership. A non-nil transform rewrites the visible thinking.
func ownedBlock(hide, owned bool, transform func(string, int) string, content []AssistantSegment) *AssistantMessageComponent {
	block := NewAssistantMessageComponent(nil, hide, nil, "", nil, nil, "", "")
	if transform != nil {
		block.SetThinkingMarkdownTransform(transform)
	}
	block.SetOwnedThinkingRuns(owned)
	block.SetContent(content)
	return block
}

var (
	thinkText        = AssistantSegment{Thinking: true, Text: "deep thoughts"}
	answerText       = AssistantSegment{Text: "answer"}
	toolCallBoundary = AssistantSegment{}
)

// Both trigger paths of issue #384 and the cases around them: what a message draws when its thinking run is rewritten to
// nothing (visible run) or hidden, with the extension owning thinking runs and without.
func TestOwnedThinkingRunsLeaveNoSpacing(t *testing.T) {
	cases := []struct {
		name      string
		hide      bool
		transform func(string, int) string
		content   []AssistantSegment
		terminal  [2]string // stopReason, errorMessage
		stock     []string  // rows when no extension owns thinking runs (Pi)
		owned     []string  // rows when one does
	}{
		{name: "thinking then text, transformer renders the run empty", transform: emptyThinkingTransform,
			content: []AssistantSegment{thinkText, answerText},
			stock:   []string{"", "", " answer"}, owned: []string{"", " answer"}},
		{name: "thinking only, transformer renders the run empty", transform: emptyThinkingTransform,
			content: []AssistantSegment{thinkText},
			stock:   []string{""}, owned: nil},
		{name: "text, empty run, text", transform: emptyThinkingTransform,
			content: []AssistantSegment{{Text: "one"}, thinkText, {Text: "two"}},
			stock:   []string{"", " one", "", " two"}, owned: []string{"", " one", " two"}},
		{name: "two empty runs split by a tool call do not accumulate spacers", transform: emptyThinkingTransform,
			content: []AssistantSegment{thinkText, toolCallBoundary, thinkText, answerText},
			stock:   []string{"", "", "", " answer"}, owned: []string{"", " answer"}},
		{name: "empty run before an error diagnostic", transform: emptyThinkingTransform,
			content:  []AssistantSegment{thinkText},
			terminal: [2]string{"error", "boom"},
			stock:    []string{"", "", " Error: boom"}, owned: []string{"", " Error: boom"}},
		{name: "hidden run with the default label", hide: true,
			content: []AssistantSegment{thinkText, answerText},
			stock:   []string{"", " Thinking...", "", " answer"}, owned: []string{"", " answer"}},
		{name: "hidden run, thinking only", hide: true,
			content: []AssistantSegment{thinkText},
			stock:   []string{"", " Thinking..."}, owned: nil},
		{name: "a visible run with content is drawn as Pi draws it",
			content: []AssistantSegment{thinkText, answerText},
			stock:   []string{"", " deep thoughts", "", " answer"}, owned: []string{"", " deep thoughts", "", " answer"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{40, 80} {
				for _, owned := range []bool{false, true} {
					block := ownedBlock(tc.hide, owned, tc.transform, tc.content)
					if tc.terminal[0] != "" {
						block.SetTerminalError(tc.terminal[0], tc.terminal[1])
					}
					want := tc.stock
					if owned {
						want = tc.owned
					}
					if got := ownedRows(block, width); !reflect.DeepEqual(got, want) {
						t.Fatalf("owned=%v width=%d: rows %q, want %q", owned, width, got, want)
					}
				}
			}
		})
	}
}

// A message that draws nothing has no OSC 133 zone, as Pi's empty message has none.
func TestOwnedThinkingRunsOnlyMessageDrawsNothing(t *testing.T) {
	block := ownedBlock(false, true, emptyThinkingTransform, []AssistantSegment{thinkText})
	if rows := block.Render(40); len(rows) != 0 {
		t.Fatalf("rows = %q, want none", rows)
	}
}

// Ctrl+T (SetHideThinkingBlock) restores the native thinking immediately, and ownership toggling restores Pi's rows.
func TestOwnedThinkingRunsToggles(t *testing.T) {
	block := ownedBlock(true, true, nil, []AssistantSegment{thinkText, answerText})
	if got, want := ownedRows(block, 40), []string{"", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden and owned: %q, want %q", got, want)
	}
	block.SetHideThinkingBlock(false)
	if got, want := ownedRows(block, 40), []string{"", " deep thoughts", "", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Ctrl+T reveals the run: %q, want %q", got, want)
	}
	block.SetHideThinkingBlock(true)
	block.SetOwnedThinkingRuns(false)
	if got, want := ownedRows(block, 40), []string{"", " Thinking...", "", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ownership off restores Pi's rows: %q, want %q", got, want)
	}
	block.SetOwnedThinkingRuns(true)
	if got, want := ownedRows(block, 40), []string{"", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ownership on again: %q, want %q", got, want)
	}
}

// Clicking a visible run hides it as in Pi; an owned run then draws nothing, and Ctrl+T brings it back.
func TestOwnedThinkingRunsKeepClickToHide(t *testing.T) {
	block := ownedBlock(false, true, nil, []AssistantSegment{thinkText, answerText})
	rows := ownedRows(block, 80)
	if want := []string{"", " deep thoughts", "", " answer"}; !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows %q, want %q", rows, want)
	}
	event := componentMouseEvent(MouseClick, 2, 1)
	event.Height = len(rows)
	if result := DispatchMouseEvent(block, event); result == nil || !result.Handled {
		t.Fatalf("click on the run was not handled: %+v", result)
	}
	if got, want := ownedRows(block, 80), []string{"", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("clicked run: %q, want %q", got, want)
	}
	block.SetHideThinkingBlock(false)
	if got, want := ownedRows(block, 80), []string{"", " deep thoughts", "", " answer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Ctrl+T after a click: %q, want %q", got, want)
	}
}

// A run whose rewrite has not been published draws no rows yet, so an owned run leaves no spacing until it does; the text never
// loses its leading spacer meanwhile. Once the rewrite is empty the run stays absent; once it has content the spacing comes with it.
func TestOwnedThinkingRunsAsyncRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, rewrite string
		want          []string
	}{
		{"empty rewrite", "", []string{"", " answer"}},
		{"rewrite with content", "rewritten", []string{"", " rewritten", "", " answer"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var workers sync.WaitGroup
			release := make(chan struct{})
			block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
			block.SetAsyncMarkdownTransforms(nil, &AsyncMarkdownTransform{
				Context: t.Context(), Start: workers.Go,
				Prepare: func(string, int) func(context.Context) string {
					return func(context.Context) string { <-release; return tc.rewrite }
				},
			})
			block.SetOwnedThinkingRuns(true)
			block.SetContent([]AssistantSegment{thinkText, answerText})
			if got, want := ownedRows(block, 40), []string{"", " answer"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("pending: %q, want %q", got, want)
			}
			close(release)
			workers.Wait()
			if got := ownedRows(block, 40); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("published: %q, want %q", got, tc.want)
			}
		})
	}
}
