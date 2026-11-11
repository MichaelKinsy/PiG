package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// BenchmarkCompleteRowRedraw is one redraw of a finished write call (a 1500-line Go file) and of a running codemode call
// (a 1500-line script): the redraw a progress update, a timer tick or an expand toggle causes.
func BenchmarkCompleteRowRedraw(b *testing.B) {
	writeArgs, err := json.Marshal(map[string]string{"path": "sample.go", "content": writeSource(1500)})
	if err != nil {
		b.Fatal(err)
	}
	var script strings.Builder
	for i := range 1500 {
		fmt.Fprintf(&script, "const v%d = await tools.read({ path: \"file%d.ts\" }); // note\n", i, i)
	}
	codemodeArgs, err := json.Marshal(map[string]string{"code": script.String()})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("write", func(b *testing.B) {
		context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", ArgsComplete: true}
		writeRenderCall(writeArgs, nil, context).Render(120)
		b.ReportAllocs()
		for b.Loop() {
			context.LastComponent = writeRenderCall(writeArgs, nil, context)
			context.LastComponent.Render(120)
		}
	})
	b.Run("codemode", func(b *testing.B) {
		context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", ArgsComplete: true, ExecutionStarted: true}
		codemodeRenderCall(codemodeArgs, nil, context).Render(120)
		b.ReportAllocs()
		for b.Loop() {
			codemodeRenderCall(codemodeArgs, nil, context).Render(120)
		}
	})
}
