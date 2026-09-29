package codingagent

import (
	"context"
	"sync"
	"time"
)

// suspendOperations separates process signals and terminal ownership from the suspend state machine. The continuation registration owns cancellation and dispatches successful continuation on the UI owner.
type suspendOperations struct {
	keepAlive       func(time.Duration) func()
	ignoreInterrupt func() func()
	continued       func(onContinue func() error, onCancel func()) func()
	stop            func()
	start           func() error
	requestRender   func()
	kill            func(int) error
}

// suspendTerminal registers continuation before stopping the UI and returns when signal delivery returns. Only the later SIGCONT callback restarts the UI; cancellation and failure release temporary ownership without restarting it.
// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts: handleCtrlZ.
func suspendTerminal(ctx context.Context, platform string, showStatus func(string), ops suspendOperations) error {
	if platform == "windows" {
		showStatus("Suspend to background is not supported on Windows")
		return nil
	}
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:handleCtrlZ
	stopKeepAlive := ops.keepAlive((1 << 30) * time.Millisecond)
	restoreInterrupt := ops.ignoreInterrupt()
	registered := make(chan struct{})
	var stopContinue func()
	var once sync.Once
	cleanup := func() bool {
		<-registered
		cleaned := false
		once.Do(func() {
			stopKeepAlive()
			restoreInterrupt()
			stopContinue()
			cleaned = true
		})
		return cleaned
	}
	stopContinue = ops.continued(func() error {
		if !cleanup() || ctx.Err() != nil {
			return nil
		}
		if err := ops.start(); err != nil {
			return err
		}
		ops.requestRender()
		return nil
	}, func() { cleanup() })
	close(registered)
	if ctx.Err() != nil {
		cleanup()
		return context.Cause(ctx)
	}
	ops.stop()
	if err := ops.kill(0); err != nil {
		cleanup()
		return err
	}
	return nil
}
