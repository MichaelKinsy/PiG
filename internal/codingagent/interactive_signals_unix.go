//go:build unix

package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleCtrlZ; the resize handler is the pig terminal wiring of the same lifecycle).

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// installResizeHandler drives onTerminalResize from SIGWINCH. Returns a stop
// func; the goroutine also exits when ctx is cancelled.
//
// While it runs, the process terminal keeps its size between SIGWINCHs, as
// Node keeps process.stdout.columns and rows, instead of reading it for every
// frame; each SIGWINCH drops the kept size before the resize render is posted.
func (m *InteractiveMode) installResizeHandler(ctx context.Context) func() {
	invalidateSize, stopSize := func() {}, func() {}
	if m.tuiInst != nil {
		if terminal, ok := m.tuiInst.Terminal().(*tui.ProcessTerminal); ok {
			invalidateSize, stopSize = terminal.CacheSizeUntilResize()
		}
	}
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winchCh:
				invalidateSize()
				m.onTerminalResize()
			}
		}
	}()
	return func() { signal.Stop(winchCh); stopSize() }
}

// installContinueHandler arms the terminal again after a continue that the Ctrl+Z path did not arrange: an external kill -TSTP stops the process by the kernel's default action with the terminal armed, the shell takes the tty for its prompt, and fg continues a process whose raw mode, bracketed paste, mouse and keyboard modes are not what the shell left. Returns a stop func.
//
// The handler is the terminal ownership's: tui calls it for a SIGCONT only while a renderer owns the terminal, so there is no effect before the first start, after a stop, while an external editor or any child owns the terminal, or while the Ctrl+Z path waits for its own SIGCONT (onSuspendContinue, which restarts the renderer itself). The re-arm runs on the owner loop, and a burst of signals is one re-arm.
// pig divergence (D661): Pi main stops the TUI from a SIGTSTP listener and then stops the process (interactive-mode.ts handleCtrlZ); a Go program that catches SIGTSTP cannot give the default stop back, so the stop stays the kernel's and the terminal is armed again here at the continue.
func (m *InteractiveMode) installContinueHandler(ctx context.Context) func() {
	return tui.SetContinueSignalHandler(m.continueHandler(ctx, m.rearmTerminal))
}

// continueHandler is the handler tui runs on SIGCONT: it posts rearm to the owner loop, and rearm runs there only if a renderer still owns the terminal.
// It posts on m.backgroundCtx (falling back to ctx, as onSuspendContinue does) so Stop() cancelling backgroundCtx always unblocks a post waiting on a full
// uiTaskCh before backgroundTasks.Wait().
func (m *InteractiveMode) continueHandler(ctx context.Context, rearm func()) func(owned func() bool) {
	postCtx := m.backgroundCtx
	if postCtx == nil {
		postCtx = ctx
	}
	var pending atomic.Bool
	return func(owned func() bool) {
		if pending.Swap(true) {
			return
		}
		// The post waits for the owner loop and must not hold the signal goroutine.
		m.backgroundTasks.Go(func() {
			err := m.postToMain(postCtx, func() {
				pending.Store(false)
				if owned() && postCtx.Err() == nil {
					rearm()
				}
			})
			if err != nil {
				pending.Store(false)
			}
		})
	}
}

// rearmTerminal gives the terminal back and takes it again through the operations of the Ctrl+Z resume (stop with PreserveScreen, start, full redraw),
// so raw mode, bracketed paste, keyboard protocol, mouse modes and the screen are armed again after an external stop and fg without dumping the fullscreen
// transcript into main-screen scrollback.
// pig divergence (D661): runs unconditionally on foreground SIGCONT so shells that restore a job's saved raw termios on fg (fish, mksh) still re-arm and redraw.
// pig divergence (D227): a process not in the foreground of its terminal does not touch it and stops its process group again until fg.
func (m *InteractiveMode) rearmTerminal() {
	m.rearmWithOps(m.rearmOperations())
}

func (m *InteractiveMode) rearmWithOps(ops suspendOperations) {
	foreground, err := ops.foreground()
	if err != nil {
		return
	}
	if !foreground {
		if ops.kill != nil {
			_ = ops.kill(0)
		}
		return
	}
	ops.stop()
	if err := ops.start(); err != nil {
		m.inputLoopErr = err
		return
	}
	ops.requestRender()
}

// ignoreSuspendInterrupt changes dispatch-time listener state without discarding pending kernel signals. SIGINT delivered after resume cleanup follows the ordinary interrupt handler, as in Pi's process.removeListener path.
func (m *InteractiveMode) ignoreSuspendInterrupt() func() {
	m.suspended.Store(true)
	return func() { m.suspended.Store(false) }
}

// suspendKill sends SIGTSTP to the process group of pid 0, as Pi's process.kill(0, "SIGTSTP"); a test replaces it so a key press does not stop the test binary and the processes that share its group.
var suspendKill = func(pid int) error { return syscall.Kill(pid, syscall.SIGTSTP) }

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
			// The stop for the suspend is its own transaction of the terminal: the writes of other goroutines wait behind it and run after it.
			endStop := m.holdTerminalStop()
			// pig additive (D91): a frontend session learns of the stop
			// while PiG still reads its input, and keeps its frames.
			if m.surface != nil {
				m.surface.Suspend()
			}
			if m.inputReader != nil {
				m.inputReader.pause()
			}
			if m.surface == nil {
				m.tuiInst.StopWithOptions(tui.StopOptions{ExitTerminal: true})
			}
			if m.rawRestore != nil {
				m.rawRestore()
				m.rawRestore = nil
				m.rawDrain = nil
			}
			endStop()
		},
		start: func() error {
			restore, drain, err := m.startTerminal(m.altScreen != nil)
			if err != nil {
				m.exitIfDeadTerminal(err)
				return err
			}
			m.rawRestore = restore
			m.rawDrain = drain
			if m.inputReader != nil {
				m.inputReader.resume()
			}
			if m.surface != nil {
				m.surface.Resume()
			} else {
				m.tuiInst.Start()
			}
			return nil
		},
		requestRender: func() {
			// pig additive (D91): Resume already drew what changed.
			if m.surface == nil {
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			}
		},
		kill:       suspendKill,
		foreground: func() (bool, error) { return m.foregroundOrExit(tui.StdinInForeground) },
	}
}

// rearmOperations are the stop/start/render operations used after an external SIGTSTP and fg.
// Unlike Ctrl+Z (suspendOperations), rearm does not hand the terminal to another process: it keeps the stdin reader running
// (avoiding a deadlock when SIGTSTP caught the reader between poll and read and the shell consumed the pending bytes) and calls
// tui.RearmRawMode instead of rawRestore+EnterRawMode, preserving both the saved cooked termios (critical under shells like fish/mksh
// that restore raw termios on fg) and avoiding restoring cooked mode while the reader is active. PreserveScreen keeps fullscreen
// teardown from dumping the transcript into main-screen scrollback.
func (m *InteractiveMode) rearmOperations() suspendOperations {
	return suspendOperations{
		stop: func() {
			endStop := m.holdTerminalStop()
			if m.surface != nil {
				m.surface.Suspend()
			} else {
				m.tuiInst.StopWithOptions(tui.StopOptions{PreserveScreen: true, ExitTerminal: true})
			}
			endStop()
		},
		start: func() error {
			if m.altScreen != nil {
				m.altScreen.BeforeTerminalStart()
			}
			if err := tui.RearmRawMode(); err != nil {
				if m.altScreen != nil {
					m.altScreen.StopWithOptions(tui.StopOptions{PreserveScreen: true})
				}
				m.exitIfDeadTerminal(err)
				return err
			}
			if m.surface != nil {
				m.surface.Resume()
			} else {
				m.tuiInst.Start()
			}
			return nil
		},
		requestRender: func() {
			if m.surface == nil {
				m.tuiInst.ForceFullRender()
				m.tuiInst.Render()
			}
		},
		foreground: func() (bool, error) { return m.foregroundOrExit(tui.StdinInForeground) },
		kill:       suspendKill,
	}
}

// foregroundOrExit runs the foreground probe and ends the process through the dead-terminal exit (129) when the probe fails because the terminal went away.
// pig divergence (D227): the foreground check that Pi's SIGCONT handler lacks.
func (m *InteractiveMode) foregroundOrExit(probe func() (bool, error)) (bool, error) {
	foreground, err := probe()
	if err != nil {
		m.exitIfDeadTerminal(err)
	}
	return foreground, err
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
		for {
			select {
			case <-ctx.Done():
				onCancel()
				return
			case <-continued:
				applied := make(chan struct{})
				parked := false
				if err := m.postToMain(ctx, func() {
					defer close(applied)
					if ctx.Err() != nil {
						onCancel()
						return
					}
					err := onContinue()
					// pig divergence (D227): a parked continuation keeps the waiter for the SIGCONT that fg sends.
					if errors.Is(err, errSuspendParked) {
						parked = true
						return
					}
					if err != nil {
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
					return
				}
				if !parked {
					return
				}
			}
		}
	})
	return func() {
		cancel()
		signal.Stop(continued)
	}
}
