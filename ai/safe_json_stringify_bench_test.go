package ai

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkSafeJsonStringifyTranscript is the faux provider's per-turn usage estimate: the whole transcript's tool arguments and declarations stringified.
func BenchmarkSafeJsonStringifyTranscript(b *testing.B) {
	var calls []any
	for i := range 200 {
		calls = append(calls, map[string]any{
			"id": fmt.Sprint("call_", i), "name": "bash",
			"arguments": map[string]any{"command": strings.Repeat("ls -la /tmp && ", 4), "timeout": 30, "env": map[string]any{"A": "1", "B": "two"}},
			"cost":      map[string]any{"input": 0.0003, "output": 0.0006, "total": 0.0009},
		})
	}
	b.ReportAllocs()
	for range b.N {
		if len(SafeJsonStringify(calls)) == 0 {
			b.Fatal("empty")
		}
	}
}
