package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Session files sort by localeCompare; a shared collate.Collator raced when sessions were listed concurrently (run with -race).
func TestSessionListingSortsConcurrentlyWithoutARace(t *testing.T) {
	dir := t.TempDir()
	for i := range 40 {
		name := fmt.Sprintf("2026-10-0%dT00-00-00-000Z_Sessión-%02d_%c.jsonl", i%9+1, i, 'a'+rune(i%26))
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"type":"session","version":3,"id":"x","timestamp":"2026-10-01T00:00:00.000Z","cwd":"/x"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sm := NewSessionManagerWithDir(t.TempDir(), dir)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				_, _ = sm.ListCurrentSessions()
			}
		})
	}
	wg.Wait()
}
