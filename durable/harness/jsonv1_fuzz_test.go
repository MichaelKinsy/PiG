package harness

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonseed"
)

func FuzzCallArgumentsMatchesV1(f *testing.F) {
	jsonseed.Add(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		var call ai.ToolCall
		if err := call.SetArgumentsJSON(data); err != nil {
			t.Skip("the arguments of a tool call are a complete JSON object")
		}
		jsonseed.SameResult(t, data, "callArguments", callArguments(call), callArgumentsV1(call))
	})
}

func BenchmarkCallArguments(b *testing.B) {
	var call ai.ToolCall
	if err := call.SetArgumentsJSON(jsonseed.Typical()); err != nil {
		b.Fatal(err)
	}
	jsonseed.Bench(b, nil, func([]byte) { callArgumentsV1(call) }, func([]byte) { callArguments(call) })
}
