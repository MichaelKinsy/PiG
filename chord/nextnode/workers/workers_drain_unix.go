//go:build unix

// SPDX-License-Identifier: MIT

package workers

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// drainSource is a stream whose reads can be made without waiting and whose waiting read can be woken: a pipe (*os.File)
// and a Unix socket (*net.UnixConn) are.
type drainSource interface {
	io.Reader
	SetReadDeadline(deadline time.Time) error
	SyscallConn() (syscall.RawConn, error)
}

// longAgo is a read deadline that has passed: setting it wakes a read that waits for its stream (net/http's aLongTimeAgo,
// which aborts a pending read the same way). It is a point in time, not a wait.
var longAgo = time.Unix(1, 0)

// reader is source as the drain's goroutine reads it: each read looks at the stream without waiting, and waits for it only
// when there is nothing. A source that cannot be read so is read as it is, and its drain settles at its end.
func (d *drain) reader(source io.Reader) io.Reader {
	stream, ok := source.(drainSource)
	if !ok {
		return source
	}
	raw, err := stream.SyscallConn()
	if err != nil {
		return source
	}
	d.mu.Lock()
	d.wake = func() { _ = stream.SetReadDeadline(longAgo) }
	d.mu.Unlock()
	return &drainReader{drain: d, source: stream, raw: raw}
}

// drainReader reads a drain's stream for its goroutine.
type drainReader struct {
	drain  *drain
	source drainSource
	raw    syscall.RawConn
}

func (r *drainReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	for {
		var count int
		var readErr error
		err := r.raw.Read(func(descriptor uintptr) bool {
			// Loaded before the read: what the process wrote is in the stream once it ended, so a read that began after its
			// end and finds nothing has it all. Loaded after, the read could have been made before the process's last write.
			ended := r.drain.ended.Load()
			for {
				count, readErr = syscall.Read(int(descriptor), buffer)
				if !errors.Is(readErr, syscall.EINTR) {
					break
				}
			}
			if !errors.Is(readErr, syscall.EAGAIN) {
				return true
			}
			if ended {
				r.drain.settle()
			}
			return false
		})
		switch {
		case errors.Is(err, os.ErrDeadlineExceeded):
			// processEnded woke the wait: its deadline is taken back and the stream is looked at again.
			_ = r.source.SetReadDeadline(time.Time{})
		case err != nil:
			return 0, err
		case readErr != nil:
			return 0, readErr
		case count == 0:
			return 0, io.EOF
		default:
			return count, nil
		}
	}
}
