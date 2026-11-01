//go:build !race

package codingagent

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// flushHighlightMemo empties the process-wide memo of tui.HighlightCode, which holds 1024 entries at most, so only a
// row's own CodeHighlighter can still serve its code.
func flushHighlightMemo() {
	for i := range 1100 {
		tui.HighlightCode(fmt.Sprintf("var flushed%d = %d", i, i), "go")
	}
}

// redrawBytes is the bytes one redraw allocates once the memo is flushed, measured over several redraws.
func redrawBytes(redraw func()) uint64 {
	const redraws = 8
	var total uint64
	for range redraws {
		flushHighlightMemo()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		redraw()
		runtime.ReadMemStats(&after)
		total += after.TotalAlloc - before.TotalAlloc
	}
	return total / redraws
}

// Ports the "write highlights unchanged first lines and a complete file only once" and "codemode highlights the script
// once" tests of packages/coding-agent/test/code-highlighter.test.ts. A Go row cannot count highlight.js calls, because
// the memo of tui.HighlightCode answers a repeated call; the rows keep their highlighting in a tui.CodeHighlighter,
// which stays valid when the memo is flushed, so a redraw with a flushed memo allocates what decoding the arguments
// costs and not what highlighting the code costs.
func TestCompleteWriteRowReusesItsHighlightingAcrossRedraws(t *testing.T) {
	content := writeSource(1500)
	args, err := json.Marshal(map[string]string{"path": "sample.go", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{}
	context := extension.ToolRenderContext{State: state, Cwd: "/work", ArgsComplete: true}
	first := strings.Join(writeRenderCall(args, nil, context).Render(120), "\n")
	context.Expanded = true
	writeRenderCall(args, nil, context).Render(120)
	context.Expanded = false
	if perRedraw := redrawBytes(func() { writeRenderCall(args, nil, context).Render(120) }); perRedraw > 6*uint64(len(args)) {
		t.Fatalf("a redraw of a complete write call of %d bytes allocated %d bytes: the row highlighted its file again", len(args), perRedraw)
	}
	if again := strings.Join(writeRenderCall(args, nil, context).Render(120), "\n"); again != first {
		t.Fatalf("a redraw changed the row\n got %q\nwant %q", again, first)
	}
}

func TestCodemodeRowReusesItsScriptHighlightingAcrossRedraws(t *testing.T) {
	var script strings.Builder
	for i := range 1500 {
		fmt.Fprintf(&script, "const v%d = await tools.read({ path: \"file%d.ts\" }); // note\n", i, i)
	}
	args, err := json.Marshal(map[string]string{"code": script.String()})
	if err != nil {
		t.Fatal(err)
	}
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", ArgsComplete: true, ExecutionStarted: true}
	first := strings.Join(codemodeRenderCall(args, nil, context).Render(120), "\n")
	// The redraw still joins the highlighted lines and previews them, as renderer.ts does (about 11 times the script);
	// highlighting the script again costs about 650 times.
	if perRedraw := redrawBytes(func() { codemodeRenderCall(args, nil, context).Render(120) }); perRedraw > 30*uint64(len(args)) {
		t.Fatalf("a redraw of a codemode script of %d bytes allocated %d bytes: the row highlighted its script again", len(args), perRedraw)
	}
	if again := strings.Join(codemodeRenderCall(args, nil, context).Render(120), "\n"); again != first {
		t.Fatalf("a redraw changed the row\n got %q\nwant %q", again, first)
	}
}
