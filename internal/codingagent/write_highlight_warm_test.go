package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// streamWriteCard draws a write call of a TypeScript file as the interactive mode does: a frame per step bytes of content while the
// arguments stream in, then the frame of the complete call. It returns the lines of that last frame, collapsed and expanded, and the
// card's state.
func streamWriteCard(t *testing.T, content string, step int) (collapsed, expanded []string, state *builtInRenderState) {
	t.Helper()
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work"}
	draw := func(text string, complete bool) []string {
		quoted, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		context.ArgsComplete = complete
		context.LastComponent = writeRenderCall(json.RawMessage(`{"path":"big/source.ts","content":`+string(quoted)+`}`), nil, context)
		return context.LastComponent.Render(160)
	}
	for end := step; end < len(content); end += step {
		draw(content[:end], false)
	}
	// The warm-ups run on the card's worker: the call completes after the last one, as a stream whose last chunk takes longer
	// than a warm-up does.
	builtInState(context).tasks.Wait()
	collapsed = draw(content, true)
	context.Expanded = true
	expanded = draw(content, true)
	return collapsed, expanded, builtInState(context)
}

// forgetHighlights drops the process-wide highlight memo and resume states, as a theme change does, so the next highlight of a text
// starts from its first line.
func forgetHighlights(t *testing.T) {
	t.Helper()
	name := tui.ActiveTheme().Name
	tui.SetTheme("light")
	tui.SetTheme(name)
	tui.HighlightCode("const x = 1", "typescript")
}

// A write call that streams in a file warms the highlight of the whole file a step at a time, so the frame that completes the call
// continues from the last step. The lines that frame draws, collapsed and expanded, are those of a call that highlights the whole file
// once, as Pi's does.
func TestWarmedWriteCardDrawsTheLinesOfAWholeRun(t *testing.T) {
	content := streamedWriteTypeScript(120)

	forgetHighlights(t)
	_, _, cold := streamWriteCard(t, content, len(content)+1) // no streaming frame: the complete call highlights the file in one run
	if cold.writeWarmed != 0 {
		t.Fatalf("a call that never streamed warmed the highlight to %d bytes", cold.writeWarmed)
	}
	forgetHighlights(t)
	wantCollapsed, wantExpanded, _ := streamWriteCard(t, content, len(content)+1)

	forgetHighlights(t)
	gotCollapsed, gotExpanded, warm := streamWriteCard(t, content, 1000)
	if warm.writeWarmOff || warm.writeWarmed < len(content)-writeWarmStep(len(content)) {
		t.Fatalf("the warm-ups stopped at %d of %d bytes (off: %v)", warm.writeWarmed, len(content), warm.writeWarmOff)
	}
	if !slices.Equal(gotCollapsed, wantCollapsed) {
		t.Errorf("collapsed card differs after warm-ups:\n got %q\nwant %q", gotCollapsed, wantCollapsed)
	}
	if !slices.Equal(gotExpanded, wantExpanded) {
		t.Errorf("expanded card differs after warm-ups (%d lines, want %d)", len(gotExpanded), len(wantExpanded))
	}
	if !strings.Contains(strings.Join(gotCollapsed, "\n"), "more lines") {
		t.Fatalf("the collapsed card names no hidden lines:\n%s", strings.Join(gotCollapsed, "\n"))
	}
}

// A path whose language highlight.js lacks highlights nothing as a whole file, so a streaming call warms nothing.
func TestWriteCardWarmsNothingForAPathWithoutALanguage(t *testing.T) {
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work"}
	content := strings.Repeat("plain text line\n", 4000)
	quoted, _ := json.Marshal(content)
	writeRenderCall(json.RawMessage(`{"path":"notes.unknownext","content":`+string(quoted)+`}`), nil, context)
	if state := builtInState(context); state.writeWarmed != 0 {
		t.Fatalf("a file without a language was warmed to %d bytes", state.writeWarmed)
	}
}
