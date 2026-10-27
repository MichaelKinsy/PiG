package sdk

import (
	"fmt"
	"testing"
)

// benchSessionEntries is a linear session of n message entries with a compaction a quarter of the way in and a model change at the start.
func benchSessionEntries(n int) []map[string]any {
	entries := make([]map[string]any, 0, n+2)
	parent := any(nil)
	add := func(entry map[string]any) {
		entry["id"] = fmt.Sprintf("e%d", len(entries))
		entry["parentId"] = parent
		entry["timestamp"] = "2026-01-02T03:04:05.678Z"
		parent = entry["id"]
		entries = append(entries, entry)
	}
	add(map[string]any{"type": "model_change", "provider": "anthropic", "modelId": "claude"})
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		add(map[string]any{"type": "message", "message": map[string]any{"role": role, "content": "hello", "timestamp": float64(i)}})
	}
	return entries
}

func BenchmarkBuildSessionContext(b *testing.B) {
	for _, n := range []int{10, 1000} {
		entries := benchSessionEntries(n)
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := BuildSessionContext(entries, SessionLeafLast, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
