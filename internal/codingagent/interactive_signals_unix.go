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

// installTerminalGoneHandler routes SIGHUP (SSH disconnect, window close) to
// the bounded terminal-gone shutdown. Returns a stop func. The goroutine also
// exits when ctx is cancelled.
func (m *InteractiveMode) installTerminalGoneHandler(ctx context.Context) func() {
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-hupCh:
			m.handleTerminalGone()
		}
	}()
	return func() { signal.Stop(hupCh) }
}

// handleTerminalGone runs the bounded terminal-gone shutdown: give extensions
// a BOUNDED window to run cleanup that does not touch the tty (remove sockets,
// flush state), then hard-exit WITHOUT terminal restore (restore writes to a
// dead tty re-trigger EIO/EPIPE). The timeout guarantees we exit even if a
// handler blocks on the dead terminal. Mirrors upstream's
// session_shutdown-before-exit intent (interactive-mode.ts:3378), bounded for
// the dead-tty case. Only SIGHUP reaches it: upstream registers SIGHUP
// only off win32, where console close arrives as SIGTERM.
func (m *InteractiveMode) handleTerminalGone() {
	done := make(chan struct{})
	go func() {
		m.ShutdownFromSignal()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	os.Exit(129)
}

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

// handleSuspend drops pig to the shell via SIGTSTP and resumes cleanly when the
// user `fg`s back. Mirrors upstream `packages/tui/src/tui.ts` SIGTSTP handler.
// Steps:
//
//  1. Pop raw-mode (writes the bracketed-paste / modifyOtherKeys disable
//     sequences and restores cooked termios) so the shell sees a normal
//     terminal. Without this, the user's prompt would come back in raw mode and
//     most keystrokes would be silently dropped.
//  2. SIGTSTP to self. The kernel parks the process. Control returns to the
//     parent shell.
//  3. On `fg`, execution resumes here. Re-enter raw mode, re-install the same
//     restore closure, and force a full re-render so the chat surface, status
//     line, and editor box come back exactly as they were.
//
// In-flight agent operations are NOT cancelled: the LLM goroutine keeps
// running while suspended. Errors during raw-mode re-entry are surfaced to the
// chat surface rather than fatal-ing.
func (m *InteractiveMode) handleSuspend() {
	m.suspended.Store(true)
	defer m.suspended.Store(false)
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
	}
	// Send SIGTSTP to this process group, mirroring upstream's
	// `process.kill(0, "SIGTSTP")`. In practice the group holds only pig: bash
	// tools and extension subprocesses are spawned into their own groups here
	// and upstream detaches them too (`bash.ts` spawns with
	// `detached: platform !== "win32"`), so a long-running tool keeps executing
	// while pig is parked in both systems. The kernel parks the job; on `fg`
	// execution resumes from the next line.
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		m.appendToChat(tui.NewText("\033[33msuspend failed: " + err.Error() + "\033[0m"))
	}
	restore, err := tui.EnterRawMode()
	if err != nil {
		m.appendToChat(tui.NewText("\033[31msuspend resume failed (terminal not raw): " + err.Error() + "\033[0m"))
		return
	}
	m.rawRestore = restore
	m.tuiInst.Render()
}
