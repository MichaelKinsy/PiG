//go:build !race && linux

package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// proxyLongSource is the content of a long file a model writes: source code with spaces, braces and newlines to escape.
func proxyLongSource(bytes int) string {
	var source strings.Builder
	for line := 0; source.Len() < bytes; line++ {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", line, line%97, line)
	}
	return source.String()[:bytes]
}

// proxyDeltaRun returns a run that converts the opening of a block and then units deltas of 16 bytes of the block's text through a fresh converter.
func proxyDeltaRun(t *testing.T, kind string, text string) func(units int) {
	return func(units int) {
		converter := &proxyEventConverter{partial: newProxyPartial(proxyTestModel()), toolJSON: map[int]string{}}
		start := ProxyAssistantMessageEvent{Type: ProxyAssistantMessageEventType(kind + "_start"), ContentIndex: 0}
		if kind == "toolcall" {
			start.ID, start.ToolName = "call_0", "write"
		}
		if _, err := converter.process(start); err != nil {
			t.Fatal(err)
		}
		for unit := range units {
			if _, err := converter.process(ProxyAssistantMessageEvent{Type: ProxyAssistantMessageEventType(kind + "_delta"), ContentIndex: 0, Delta: text[16*unit : 16*unit+16]}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A long block streamed through StreamProxy costs each delta the same whatever came before it: the text, the thinking and a tool call's argument text grow in place instead of being copied whole per delta, and the arguments parser reads only the new bytes. The gate compares the deltas after 1,600 and after 3,200 (the run reaches 100 KB); a whole copy or parse per delta doubles the second.
func TestStreamProxyPerDeltaAllocationGate(t *testing.T) {
	const n = 1600
	source := proxyLongSource(16 * 4 * n)
	arguments, err := json.Marshal(map[string]string{"path": "src/long.ts", "content": source})
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range []struct{ kind, text string }{{"text", source}, {"thinking", source}, {"toolcall", string(arguments)}} {
		t.Run(shape.kind, func(t *testing.T) {
			id := "agent.proxy." + shape.kind + "_delta"
			marginal := perfgate.CheckLinear(t, id, n, 1.5, proxyDeltaRun(t, shape.kind, shape.text))
			perfgate.Check(t, id+".allocs", marginal.Allocs)
			perfgate.Check(t, id+".bytes", marginal.Bytes)
		})
	}
}
