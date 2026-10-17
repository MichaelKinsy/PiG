package codingagent

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// renderBytesPerCall is the bytes one call of render allocates, averaged over runs.
func renderBytesPerCall(runs int, render func()) float64 {
	render()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range runs {
		render()
	}
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / float64(runs)
}

// An edit card's renderers run on every update of the card (each partial result, the preview landing, a theme change).
// When the arguments have not changed they must not decode and re-encode all of them to tell: the cost of renderCall and
// renderResult with unchanged arguments stays the same for 1 KiB and for 256 KiB of edits.
func TestEditRenderersDoNotReencodeUnchangedArgs(t *testing.T) {
	edit := func(size int) json.RawMessage {
		text := strings.Repeat("old line of text that the edit replaces\n", size/40)
		args, err := json.Marshal(map[string]any{"path": "src/a.go", "edits": []map[string]string{{"oldText": text, "newText": text + "x"}}})
		if err != nil {
			t.Fatal(err)
		}
		return args
	}
	cost := func(args json.RawMessage) (call, result float64) {
		context := extension.ToolRenderContext{Args: args, State: map[string]any{}, Cwd: t.TempDir()}
		call = renderBytesPerCall(50, func() { editRenderCall(args, nil, context) })
		context.IsPartial = true
		result = renderBytesPerCall(50, func() {
			editRenderResult(extension.AgentToolResult{}, extension.ToolRenderResultOptions{}, nil, context)
		})
		return call, result
	}
	smallCall, smallResult := cost(edit(1 << 10))
	largeCall, largeResult := cost(edit(256 << 10))
	t.Logf("renderCall %.0f B at 1 KiB, %.0f B at 256 KiB; renderResult %.0f B, %.0f B", smallCall, largeCall, smallResult, largeResult)
	if largeCall > 2*smallCall+1024 || largeResult > 2*smallResult+1024 {
		t.Fatalf("edit renderers re-read unchanged arguments: renderCall %.0f B at 1 KiB, %.0f B at 256 KiB; renderResult %.0f B, %.0f B", smallCall, largeCall, smallResult, largeResult)
	}
}

// The edit card's call box shows the diff preview once it lands. Drawing the box again at the same width, with the same
// preview, state and theme, must not draw the diff again: the cost of a draw is the same for a 1 KiB and a 256 KiB diff.
func TestEditPreviewBoxDrawsWithoutRedrawingTheDiff(t *testing.T) {
	cost := func(size int) float64 {
		var diff strings.Builder
		for line := 1; diff.Len() < size; line++ {
			fmt.Fprintf(&diff, "-%d old line of text that the edit replaced\n+%d new line of text that the edit wrote\n", line, line)
		}
		args := json.RawMessage(`{"path":"src/a.go","edits":[{"oldText":"a","newText":"b"}]}`)
		context := extension.ToolRenderContext{Args: args, State: map[string]any{}, Cwd: t.TempDir()}
		component := editRenderCall(args, nil, context)
		state := builtInState(context)
		state.mu.Lock()
		state.editPreview = tools.EditDiffResult{Diff: diff.String()}
		state.mu.Unlock()
		return renderBytesPerCall(20, func() { component.Render(100) })
	}
	small, large := cost(1<<10), cost(256<<10)
	t.Logf("%.0f B per draw with a 1 KiB diff, %.0f B with a 256 KiB diff", small, large)
	if large > 2*small+1024 {
		t.Fatalf("a draw redraws the diff: %.0f B with a 1 KiB diff, %.0f B with a 256 KiB diff", small, large)
	}
}
