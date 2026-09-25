package tui

import (
	"testing"
	"time"
)

// Regression guard for the per-child render-cache freeze bug: a running bash
// tool card recomputes its live "Elapsed X.Xs" footer every frame from the
// 100ms tick loop, which does not Invalidate it. If IsDirty() did not report
// the live state, the container cache would freeze the elapsed counter.
func TestToolExecutionIsDirtyWhileRunningBash(t *testing.T) {
	c := NewToolExecutionComponent("bash", "sleep 5")
	c.StartedAt = time.Now()
	c.NeedsRedraw() // clear any construction-time dirty flag
	if !c.IsDirty() {
		t.Fatal("running bash tool must report dirty so its live elapsed re-renders")
	}

	c.SetResult("done", false, time.Second)
	c.NeedsRedraw() // consume the SetResult invalidation
	if c.IsDirty() {
		t.Fatal("completed bash tool must be clean so the cache can reuse it")
	}
}

// A running non-bash tool has no wall-clock footer, so it must be cacheable
// (IsDirty false once settled): the cache optimization depends on this.
func TestToolExecutionNonBashCacheableWhenSettled(t *testing.T) {
	c := NewToolExecutionComponent("read", "file.go")
	c.StartedAt = time.Now()
	c.NeedsRedraw()
	if c.IsDirty() {
		t.Fatal("running non-bash tool has no live footer; it must be cacheable")
	}
}

// Regression guard: a running bash-execution block embeds an animated spinner
// advanced by the tick loop without Invalidating the block. IsDirty must report
// the running state so the cache does not freeze the spinner.
func TestBashExecutionBlockIsDirtyWhileRunning(t *testing.T) {
	b := NewBashExecutionBlock("sleep 5", false)
	b.NeedsRedraw()
	if !b.IsDirty() {
		t.Fatal("running bash-execution block must report dirty so its spinner animates")
	}

	zero := 0
	b.SetComplete(&zero, false, false)
	b.NeedsRedraw()
	if b.IsDirty() {
		t.Fatal("completed bash-execution block must be clean so the cache can reuse it")
	}
}
