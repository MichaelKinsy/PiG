package tools

import (
	"os"
	"sync/atomic"
	"testing"
)

// Counted work for the single-emitter scheduler: while the consumer is
// blocked, a burst of chunks coalesces into one pending update rather than
// starting work per chunk.
func TestShellUpdateSchedulerCountsWork(t *testing.T) {
	acc := NewOutputAccumulator("pi-test")
	var spillPath string
	t.Cleanup(func() {
		if err := acc.CloseTempFile(); err != nil {
			t.Errorf("close spill file: %v", err)
		}
		if spillPath != "" {
			if err := os.Remove(spillPath); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove spill file: %v", err)
			}
		}
	})

	var updates atomic.Int64
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	u := newShellUpdateScheduler(acc, func(string, any) {
		if updates.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
	})

	acc.Append([]byte("x\n"))
	u.schedule()
	<-firstStarted

	const chunks = 10000
	for range chunks - 1 {
		acc.Append([]byte("x\n"))
		u.schedule()
	}
	if got := updates.Load(); got != 1 {
		t.Errorf("updates while the consumer is blocked = %d, want 1", got)
	}
	spillPath = acc.Snapshot(false).FullOutputPath
	if spillPath == "" {
		t.Error("burst did not create the expected spill file")
	}

	acc.Finish()
	close(releaseFirst)
	u.finish()
	if got := updates.Load(); got != 2 {
		t.Errorf("updates for %d chunks = %d, want first plus final", chunks, got)
	}
}
