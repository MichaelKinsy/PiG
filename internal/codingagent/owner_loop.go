package codingagent

import (
	"bytes"
	"context"
	"runtime"
)

// currentGoroutineID returns the calling goroutine's runtime id.
func currentGoroutineID() uint64 {
	var buf [40]byte
	header := buf[:runtime.Stack(buf[:], false)]
	header, _ = bytes.CutPrefix(header, []byte("goroutine "))
	var id uint64
	for _, digit := range header {
		if digit < '0' || digit > '9' {
			break
		}
		id = id*10 + uint64(digit-'0')
	}
	return id
}

// enterOwnerLoop records the calling goroutine as the owner loop and returns the function that restores the previous owner. Nested input loops run on the owner goroutine, so they leave the owner unchanged.
func (m *InteractiveMode) enterOwnerLoop() (leave func()) {
	previous := m.ownerGoroutine.Swap(currentGoroutineID())
	return func() { m.ownerGoroutine.Store(previous) }
}

// onOwnerLoop reports whether the caller is the goroutine running the owner loop. Upstream's single JS event loop makes that true for every caller, so its synchronous reads of UI state never queue; Go has several goroutines, so a caller on the loop must run owner work inline. It is false when no loop is running.
func (m *InteractiveMode) onOwnerLoop() bool {
	owner := m.ownerGoroutine.Load()
	return owner != 0 && owner == currentGoroutineID()
}

// runOnOwner runs fn on the owner loop. A caller already on the loop runs fn inline, because queueing a task and waiting for its result from the loop itself waits for work only that goroutine can run. Other callers queue fn as runOnMain does and do not wait for it.
func (m *InteractiveMode) runOnOwner(ctx context.Context, fn func()) {
	if m.onOwnerLoop() {
		fn()
		return
	}
	m.runOnMain(ctx, fn)
}
