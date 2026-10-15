package codingagent

import (
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var idleCollectPool sync.Pool

// An idle collection (D172) returns what a run left in sync.Pool. One forced GC cycle only moves pool contents to their
// victim caches, where they stay live; the tmux prompt then kept about 290 KB more live heap than
// process.mem_tmux_idle.heap_live_bytes allows (6,586,560 against 6,300,552 bytes with the second cycle). The
// test runs in its own process (testenv.InOwnProcess) so that no other test's collection empties the victim cache first.
func TestIdleCollectionFreesPoolVictims(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	entry := new([1 << 16]byte)
	ref := weak.Make(entry)
	idleCollectPool.Put(entry)
	var c idleHeapCollector
	c.collect()
	if c.collects.Load() != 1 {
		t.Fatalf("idle collection collected %d times, want 1", c.collects.Load())
	}
	if ref.Value() != nil {
		t.Fatal("a sync.Pool entry is still live after an idle collection; one forced GC cycle leaves pool victims live")
	}
}
