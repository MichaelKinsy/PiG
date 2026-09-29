package codingagent

import (
	"fmt"
	"strings"
	"testing"
)

// Exercise selector construction, retained Input, rendering and callback without disk I/O.
func BenchmarkSessionSelectorRename(b *testing.B) {
	for _, size := range []int{0, 64, 65536} {
		b.Run(fmt.Sprintf("bytes-%d", size), func(b *testing.B) {
			name := strings.Repeat("a", size)
			loader := func() ([]SessionInfo, error) { return []SessionInfo{{Path: "/session.jsonl", Name: name}}, nil }
			bindings := sessionSelectorInputBindings(b)
			b.ReportAllocs()
			for b.Loop() {
				sel := newLoadedSessionSelector(loader, loader, func(_ string, value string) error {
					if value != "X"+name {
						b.Fatal("rename changed text")
					}
					return nil
				}, nil, "", bindings)
				sel.HandleInput("\x1b[114;5u")
				sel.HandleInput("X")
				sel.Render(120)
				sel.HandleInput("\r")
				sel.drainLoadUpdates()
				sel.close()
			}
		})
	}
}
