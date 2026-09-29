package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Drive native file discovery, progress delivery, selection, rendering and joined teardown without Provider or transcript-turn work.
func BenchmarkSessionSelectorScopeLoading(b *testing.B) {
	for _, count := range []int{10, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := b.TempDir()
			for i := range count {
				data := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":\"s%d\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"cwd\":\"/project\"}\n{\"type\":\"message\",\"id\":\"m1\",\"parentId\":null,\"timestamp\":\"2026-01-01T00:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"hello\",\"timestamp\":0}}\n", i)
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("s%04d.jsonl", i)), []byte(data), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			manager := NewSessionManagerWithDir("/project", dir)
			loader := func(options SessionListOptions) ([]SessionInfo, error) { return manager.ListSessions(options) }
			bindings := sessionSelectorInputBindings(b)
			b.ReportAllocs()
			for b.Loop() {
				s := newSessionSelector(loader, loader, nil, nil, "", bindings)
				for s.currentLoad != nil {
					select {
					case result := <-s.loadResult(sessionScopeCurrent):
						s.finishLoad(sessionScopeCurrent, result)
					case update := <-s.work.updates:
						update()
					}
					s.Render(120)
				}
				if len(s.filtered) != count {
					b.Fatalf("loaded=%d want=%d status=%s", len(s.filtered), count, s.statusState.message)
				}
				s.close()
			}
		})
	}
}
