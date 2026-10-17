package codingagent

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A write card redraws on every streamed delta of its arguments, and the arguments arrive whole each time: the cost of one redraw is the cost of reading a growing JSON object, so it is measured at the size of a large file.
func BenchmarkWriteRenderCallLargeArguments(b *testing.B) {
	for _, lines := range []int{200, 4000} {
		b.Run(strconv.Itoa(lines)+"-lines", func(b *testing.B) {
			args, err := json.Marshal(map[string]string{"path": "sample.txt", "content": strings.Repeat("a line of the file being written\n", lines)})
			if err != nil {
				b.Fatal(err)
			}
			state := map[string]any{}
			context := extension.ToolRenderContext{State: state, Cwd: "/work", IsPartial: true}
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				writeRenderCall(args, nil, context).Render(120)
			}
		})
	}
}
