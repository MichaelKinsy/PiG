//go:build unix

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
	d.holds = func() int { return streamHolds(raw) }
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
			// The look at the end and the read are one section: what a read takes after the count of what the stream held at
			// the end is taken off that count, and what it took before is not in it.
			r.drain.mu.Lock()
			defer r.drain.mu.Unlock()
			// This read began after the goroutine told what its earlier reads gave it: if that was all the stream held at the
			// end, the drain settles, and this read goes on to what a process outside the tree wrote since.
			if r.drain.ended.Load() && r.drain.owed == 0 {
				r.drain.settle()
			}
			for {
				count, readErr = syscall.Read(int(descriptor), buffer)
				if !errors.Is(readErr, syscall.EINTR) {
					break
				}
			}
			if readErr == nil {
				r.drain.took(count)
			}
			if !errors.Is(readErr, syscall.EAGAIN) {
				return true
			}
			// Nothing to read after the end: whatever the stream held was told, or it could not count what it held.
			if r.drain.ended.Load() {
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
