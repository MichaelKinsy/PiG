package codingagent

import (
	"fmt"
	"testing"
)

func BenchmarkSessionSelectorDeleteRefresh(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			rows := make([]SessionInfo, count)
			for i := range rows {
				rows[i] = scopeSession(fmt.Sprintf("session-%04d", i))
				rows[i].Name = rows[i].ID
			}
			kb := sessionSelectorInputBindings(b)
			b.ReportAllocs()
			for b.Loop() {
				refresh := false
				pending := make(chan sessionLoadResult, 1)
				loader := func(_ *sessionLoad, _ SessionListProgress) <-chan sessionLoadResult {
					if refresh {
						return pending
					}
					ready := make(chan sessionLoadResult, 1)
					ready <- sessionLoadResult{sessions: rows}
					return ready
				}
				s := newSessionSelectorWithLoaders(loader, loader, nil, unlinkDeleter(func(string) error { refresh = true; return nil }), "", kb)
				s.drainLoadUpdates()
				s.all = rows
				s.HandleInput("\x04")
				s.HandleInput("\r")
				if len(s.filtered) != count-1 {
					b.Fatalf("visible rows=%d, want %d", len(s.filtered), count-1)
				}
				s.HandleInput("\x1b")
				s.close()
			}
		})
	}
}
