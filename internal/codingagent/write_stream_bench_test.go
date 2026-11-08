package codingagent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// streamedWriteTypeScript is the file of the Pi-Bolt harness's write:N scenario (bench/fake_model_stress.py): N KB of one
// exported TypeScript function per line.
func streamedWriteTypeScript(kb int) string {
	size := kb * 1024
	var source strings.Builder
	for n := 0; source.Len() < size; n++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", n, n%97, n)
	}
	return strings.TrimSuffix(source.String(), "\n")[:size]
}

// BenchmarkStreamedWriteCard draws a collapsed write card for every update of a write call whose content streams in 16
// characters at a time, as the harness's write:N scenario does: one op is the whole stream, the content as the arguments
// carry it at each update included.
func BenchmarkStreamedWriteCard(b *testing.B) {
	for _, kb := range []int{5, 50} {
		content := streamedWriteTypeScript(kb)
		args := make([]json.RawMessage, 0, len(content)/16+1)
		for end := 16; ; end += 16 {
			end = min(end, len(content))
			quoted, err := json.Marshal(content[:end])
			if err != nil {
				b.Fatal(err)
			}
			args = append(args, json.RawMessage(`{"path":"big/source.ts","content":`+string(quoted)+`}`))
			if end == len(content) {
				break
			}
		}
		b.Run(strconv.Itoa(kb)+"KB", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work"}
				for _, update := range args {
					context.LastComponent = writeRenderCall(update, nil, context)
					context.LastComponent.Render(160)
				}
			}
		})
	}
}
