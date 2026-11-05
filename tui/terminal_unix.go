//go:build unix

package tui

// Ports packages/tui/src/terminal.ts (ProcessTerminal on Unix: raw-mode input and the terminal size).

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"sync"
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

// terminalHungUp reports whether the terminal behind file has been hung up, as when the master side of a pseudo-terminal closes.
func terminalHungUp(file *os.File) bool {
	pollfds := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(pollfds, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err == nil && n > 0 && pollfds[0].Revents&unix.POLLHUP != 0
	}
}

// inForeground is tcgetpgrp on stdin compared with this process's group: whether the group may use the terminal without a SIGTTOU or SIGTTIN
// stop. Job control applies only to the controlling terminal: ENOTTY means stdin is a terminal that is not this process's controlling terminal,
// so nothing can stop the process there and it counts as foreground, as Pi's SIGCONT handler restarts the UI there. Any other error, EIO of a
// hung-up terminal first, is returned for the caller to treat as a dead terminal.
func (t *ProcessTerminal) inForeground() (bool, error) {
	if t.stdin == nil {
		return false, syscall.ENOTTY
	}
	fd := int(t.stdin.Fd())
	for {
		pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		if isEINTR(err) {
			continue
		}
		if errors.Is(err, unix.ENOTTY) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return pgrp == syscall.Getpgrp(), nil
	}
}

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

func newTerminalInputWaiter(ctx context.Context, _ *os.File) (*terminalInputWaiter, error) {
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
func (w *terminalInputWaiter) wait(file *os.File, ms int) (bool, error) {
	for {
		pollfds := []unix.PollFd{
			{Fd: int32(file.Fd()), Events: unix.POLLIN},
			{Fd: int32(w.cancelRead.Fd()), Events: unix.POLLIN},
		}
		_, err := unix.Poll(pollfds, ms)
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

// readPipe reports no pipe: a pipe is waited on by wait like any other file, since poll answers for it.
func (w *terminalInputWaiter) readPipe(*os.File, int) ([]byte, bool, error) { return nil, false, nil }

func (w *terminalInputWaiter) close() {
	if !w.stopCallback() {
		<-w.callbackDone
	}
	_ = w.cancelRead.Close()
	_ = w.cancelWrite.Close()
}

// isEINTR reports whether a read, poll or termios ioctl error is an interrupted syscall worth retrying.
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
	refreshTerminalDimensions(false, os.Getpid(), func(pid int) error {
		return syscall.Kill(pid, syscall.SIGWINCH)
	})
	return func() { signal.Stop(winchCh) }
}

// CacheSizeUntilResize makes Columns and Rows reuse the size they last read, as Node keeps process.stdout.columns and rows
// and refreshes them only when SIGWINCH arrives (lib/tty.js WriteStream._refreshSize), instead of reading the size from the
// terminal for every frame. The caller must own SIGWINCH delivery and run invalidate on every SIGWINCH before it renders for
// it; stop ends the caching once every holder has stopped.
func (t *ProcessTerminal) CacheSizeUntilResize() (invalidate, stop func()) {
	cache := &t.sizeCache
	forget := func(holders int) {
		cache.mu.Lock()
		cache.holders += holders
		cache.generation++
		cache.valid = false
		cache.mu.Unlock()
	}
	forget(1)
	var once sync.Once
	return func() { forget(0) }, func() { once.Do(func() { forget(-1) }) }
}

// enableVTProcessing is a no-op on unix; ANSI output needs no console mode
// change. Returns a no-op restore.
func (t *ProcessTerminal) enableVTProcessing() func() { return func() {} }
