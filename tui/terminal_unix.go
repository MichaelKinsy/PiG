//go:build unix

package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// terminalInput returns the reader for keyboard input from file.
func terminalInput(file *os.File) io.Reader { return file }

// terminalInputBuffered reports whether file's reader holds input it has
// already taken from the terminal. Reads go straight to the descriptor.
func terminalInputBuffered(*os.File) bool { return false }

// terminalInputPending reports, after file became readable, whether a read
// returns without blocking. A readable descriptor always does.
func terminalInputPending(*os.File) (bool, error) { return true, nil }

// pollReadable waits up to ms milliseconds for fd to become readable. It
// returns n>0 when readable, 0 on timeout. EINTR is retried internally so the
// caller never observes it. Mirrors the original DrainInput poll loop.
func pollReadable(fd uintptr, ms int) (int, error) {
	for {
		pollfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pollfds, ms)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return 0, err
		}
		return n, nil
	}
}

type terminalInputWaiter struct {
	ctx          context.Context
	cancelRead   *os.File
	cancelWrite  *os.File
	stopCallback func() bool
	callbackDone chan struct{}
}

func newTerminalInputWaiter(ctx context.Context) (*terminalInputWaiter, error) {
	cancelRead, cancelWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	w := &terminalInputWaiter{
		ctx:          ctx,
		cancelRead:   cancelRead,
		cancelWrite:  cancelWrite,
		callbackDone: make(chan struct{}),
	}
	w.stopCallback = context.AfterFunc(ctx, func() {
		_, _ = cancelWrite.Write([]byte{0})
		close(w.callbackDone)
	})
	return w, nil
}

// wait keeps the reader outside Read while no bytes are available, so
// cancelling a stopped terminal cannot leave a stale goroutine that consumes
// the next focus owner's first key.
func (w *terminalInputWaiter) wait(file *os.File) (bool, error) {
	for {
		pollfds := []unix.PollFd{
			{Fd: int32(file.Fd()), Events: unix.POLLIN},
			{Fd: int32(w.cancelRead.Fd()), Events: unix.POLLIN},
		}
		_, err := unix.Poll(pollfds, -1)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return false, err
		}
		if w.ctx.Err() != nil || pollfds[1].Revents != 0 {
			return false, nil
		}
		return pollfds[0].Revents != 0, nil
	}
}

func (w *terminalInputWaiter) close() {
	if !w.stopCallback() {
		<-w.callbackDone
	}
	_ = w.cancelRead.Close()
	_ = w.cancelWrite.Close()
}

// isEINTR reports whether a read error is an interrupted syscall worth retrying.
func isEINTR(err error) bool { return errors.Is(err, unix.EINTR) }

// startResizeWatcher installs a SIGWINCH handler that calls onResize on resize,
// kicks once on startup (dimensions may be stale after suspend/resume), and
// returns a stop func. The goroutine also exits when ctx is cancelled.
func (t *ProcessTerminal) startResizeWatcher(ctx context.Context, onResize func()) func() {
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-winchCh:
				onResize()
			}
		}
	}()
	// Manual kick, mirroring upstream's SIGWINCH self-send on start.
	go func() { _ = syscall.Kill(os.Getpid(), syscall.SIGWINCH) }()
	return func() { signal.Stop(winchCh) }
}

// enableVTProcessing is a no-op on unix; ANSI output needs no console mode
// change. Returns a no-op restore.
func (t *ProcessTerminal) enableVTProcessing() func() { return func() {} }
