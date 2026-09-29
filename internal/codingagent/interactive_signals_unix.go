//go:build unix

package codingagent

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// installResizeHandler drives onTerminalResize from SIGWINCH. Returns a stop
// func; the goroutine also exits when ctx is cancelled.
func (m *InteractiveMode) installResizeHandler(ctx context.Context) func() {
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winchCh:
				m.onTerminalResize()
			}
		}
	}()
	return func() { signal.Stop(winchCh) }
}

// ignoreSuspendInterrupt changes dispatch-time listener state without discarding pending kernel signals. SIGINT delivered after resume cleanup follows the ordinary interrupt handler, as in Pi's process.removeListener path.
func (m *InteractiveMode) ignoreSuspendInterrupt() func() {
	m.suspended.Store(true)
	return func() { m.suspended.Store(false) }
}

// handleSuspend stops the terminal and returns after signal delivery. SIGCONT restores the terminal on the owner loop without cancelling Session work.
func (m *InteractiveMode) handleSuspend() error {
	ctx := m.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	return suspendTerminal(ctx, "unix", m.showStatus, m.suspendOperations())
}

func (m *InteractiveMode) suspendOperations() suspendOperations {
	return suspendOperations{
		keepAlive: func(interval time.Duration) func() {
			return time.NewTicker(interval).Stop
		},
		ignoreInterrupt: m.ignoreSuspendInterrupt,
		continued:       m.onSuspendContinue,
		stop: func() {
			if m.inputReader != nil {
				m.inputReader.pause()
			}
			m.tuiInst.Stop()
			if m.themeState.autoSyncEnabled.Load() {
				m.writeThemeNotifications(false)
			}
			if m.rawRestore != nil {
				m.rawRestore()
				m.rawRestore = nil
				m.rawDrain = nil
			}
		},
		start: func() error {
			restore, drain, err := tui.EnterRawModeWithDrain()
			if err != nil {
				return err
			}
			m.rawRestore = restore
			m.rawDrain = drain
			if m.inputReader != nil {
				m.inputReader.resume()
			}
			if m.themeState.autoSyncEnabled.Load() {
				m.writeThemeNotifications(true)
			}
			m.tuiInst.Start()
			return nil
		},
		requestRender: func() { m.tuiInst.ForceFullRender(); m.tuiInst.Render() },
		kill:          func(pid int) error { return syscall.Kill(pid, syscall.SIGTSTP) },
	}
}

// onSuspendContinue owns one signal waiter in the mode's joined background scope. SIGCONT uses backpressured owner dispatch; canceled or failed operations cannot restart a stopped mode.
func (m *InteractiveMode) onSuspendContinue(onContinue func() error, onCancel func()) func() {
	parent := m.backgroundCtx
	if parent == nil {
		parent = m.runCtx
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	m.backgroundTasks.Go(func() {
		defer signal.Stop(continued)
		select {
		case <-ctx.Done():
			onCancel()
		case <-continued:
			applied := make(chan struct{})
			if err := m.postToMain(ctx, func() {
				defer close(applied)
				if ctx.Err() != nil {
					onCancel()
					return
				}
				if err := onContinue(); err != nil {
					m.inputLoopErr = err
				}
			}); err != nil {
				onCancel()
				return
			}
			select {
			case <-applied:
			case <-ctx.Done():
				onCancel()
			}
		}
	})
	return func() {
		cancel()
		signal.Stop(continued)
	}
}
