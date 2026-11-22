//go:build linux || darwin

package testenv

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"
)

// PTYEOFSentinel marks the end of a child process output on a pseudo-terminal.
// Printable ASCII with no newline, carriage return, or control byte survives tty
// output processing (OPOST/ONLCR) in both raw and cooked modes; slave writes are
// output rather than input, so ECHO does not double it.
const PTYEOFSentinel = "<<PIG-PTY-EOF-7f3a9c>>"

// ReadPTYUntilSentinel drains master while keeping slave open in the parent so
// macOS does not discard unread child output when the child closes its slave
// descriptor on exit. Call finish after the child exits; it writes
// PTYEOFSentinel into slave, waits up to bound for the reader to reach it,
// closes both descriptors, and returns the bytes before the sentinel.
func ReadPTYUntilSentinel(t testing.TB, master, slave *os.File, bound time.Duration) (finish func() []byte) {
	t.Helper()
	finishRaw, cleanup := startPTYReader(t.Context(), master, slave, bound)
	t.Cleanup(cleanup)
	return func() []byte {
		t.Helper()
		data, err := finishRaw()
		if err != nil {
			t.Fatalf("%v", err)
		}
		return data
	}
}

func startPTYReader(ctx context.Context, master io.ReadCloser, slave io.WriteCloser, bound time.Duration) (finish func() ([]byte, error), cleanup func()) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	sentinel := []byte(PTYEOFSentinel)
	go func() {
		var collected []byte
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				collected = append(collected, buf[:n]...)
				if bytes.HasSuffix(collected, sentinel) {
					done <- result{data: collected[:len(collected)-len(sentinel)]}
					return
				}
			}
			if err != nil {
				done <- result{data: collected, err: err}
				return
			}
		}
	}()
	var stopped bool
	stop := func() result {
		if !stopped {
			stopped = true
			_ = master.Close()
			_ = slave.Close()
		}
		return <-done
	}
	cleanup = func() {
		if !stopped {
			stop()
		}
	}
	finish = func() ([]byte, error) {
		if _, err := io.WriteString(slave, PTYEOFSentinel); err != nil {
			res := stop()
			return res.data, fmt.Errorf("write pty sentinel: %w (collected %q)", err, res.data)
		}
		timer := time.NewTimer(bound)
		defer timer.Stop()
		select {
		case res := <-done:
			stopped = true
			_ = slave.Close()
			_ = master.Close()
			if res.err != nil {
				return res.data, fmt.Errorf("pty reader ended before sentinel: %w (collected %q)", res.err, res.data)
			}
			return res.data, nil
		case <-ctx.Done():
			res := stop()
			return res.data, fmt.Errorf("pty sentinel did not arrive before test context ended: %w (collected %q)", ctx.Err(), res.data)
		case <-timer.C:
			res := stop()
			return res.data, fmt.Errorf("pty sentinel did not arrive within %v (collected %q)", bound, res.data)
		}
	}
	return finish, cleanup
}
