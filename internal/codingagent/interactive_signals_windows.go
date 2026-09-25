//go:build windows

package codingagent

import (
	"context"
	"os"
	"time"

	"golang.org/x/term"
)

// resizePollInterval is how often the Windows resize watcher samples the
// console size. Windows has no SIGWINCH, so pig polls; ~120ms is imperceptible
// for re-render yet cheap.
const resizePollInterval = 120 * time.Millisecond

// installTerminalGoneHandler is a no-op on Windows. There is no SIGHUP; Go
// delivers console close/logoff/shutdown as SIGTERM, which main.go handles.
// Mirrors upstream's `if (!win32) signals.push("SIGHUP")`.
func (m *InteractiveMode) installTerminalGoneHandler(_ context.Context) func() {
	return func() {}
}

// installResizeHandler polls the console size and drives onTerminalResize on
// change. Returns a stop func; the goroutine also exits when ctx is cancelled.
// This is pig's mechanism for the same observable "resize -> re-render" that
// upstream gets from Node's stdout "resize" event.
func (m *InteractiveMode) installResizeHandler(ctx context.Context) func() {
	go func() {
		ticker := time.NewTicker(resizePollInterval)
		defer ticker.Stop()
		lastW, lastH, _ := term.GetSize(int(os.Stdout.Fd()))
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w, h, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					continue
				}
				if w != lastW || h != lastH {
					lastW, lastH = w, h
					m.onTerminalResize()
				}
			}
		}
	}()
	return func() {}
}

// handleSuspend shows a status message on Windows: suspend-to-background needs
// SIGTSTP job control, which Windows lacks. Mirrors upstream handleCtrlZ's
// win32 branch (interactive-mode.ts), which calls showStatus.
func (m *InteractiveMode) handleSuspend() {
	m.showStatus("Suspend to background is not supported on Windows")
}
