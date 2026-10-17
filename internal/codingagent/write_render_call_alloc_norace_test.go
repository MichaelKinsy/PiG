//go:build !race

package codingagent

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A write card redraws on every streamed delta of its arguments, and the arguments arrive whole each time. Decoding them once costs about 2.6 times their size in allocations, with the copy of the content and its lines
// included; decoding the object for the header, for the path and for the content cost 6.8 times.
func TestWriteRenderCallDecodesItsArgumentsOnce(t *testing.T) {
	args, err := json.Marshal(map[string]string{"path": "sample.txt", "content": strings.Repeat("a line of the file being written\n", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", IsPartial: true}
	writeRenderCall(args, nil, context).Render(120)
	const calls = 10
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range calls {
		writeRenderCall(args, nil, context).Render(120)
	}
	runtime.ReadMemStats(&after)
	if perCall := int((after.TotalAlloc - before.TotalAlloc) / calls); perCall > 4*len(args) {
		t.Fatalf("one redraw of %d bytes of arguments allocated %d bytes (%.1f times); decoding them once takes about 2.6 times", len(args), perCall, float64(perCall)/float64(len(args)))
	}
}
