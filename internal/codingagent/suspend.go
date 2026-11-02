package codingagent

import (
	"context"
	"errors"
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
	foreground      func() (bool, error)
}

// errSuspendParked is what a continuation returns when SIGCONT reached a process group that does not own the terminal and the session stopped again. The waiter keeps waiting for the SIGCONT that fg sends (pig divergence (D227)).
var errSuspendParked = errors.New("suspended session resumed in the background")

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
		if ctx.Err() == nil {
			// pig divergence (D227): Pi calls ui.start() on any SIGCONT. A group that does not own the terminal stops again here, before raw mode and before a frame, and waits for the SIGCONT that fg sends.
			foreground, err := ops.foreground()
			if err != nil {
				if cleanup() {
					return err
				}
				return nil
			}
			if !foreground {
				if err := ops.kill(0); err != nil {
					cleanup()
					return err
				}
				return errSuspendParked
			}
		}
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
