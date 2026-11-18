package codingagent

import (
	"encoding/json"
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
	restoreStartupTheme(t)
	name := tui.ActiveTheme().Name
	tui.SetTheme("light")
	tui.SetTheme(name)
	tui.HighlightCode("const x = 1", "typescript")
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
