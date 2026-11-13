//go:build windows

package workers

import "io"

// reader is source as the drain's goroutine reads it: source itself. Windows gives a worker's streams no read that does
// not wait (its output pipes are synchronous handles, with no deadline to wake a read and no readiness to ask for), so a
// drain there settles when its goroutine returns, at the stream's end: a worker's exit is told when its streams ended, not
// before, as Node's `close` is. That end comes with the worker: its tree is a job object that nothing it starts leaves,
// terminated at its end, so no process outside it holds a stream open (on Unix a process in a session of its own does).
func (d *drain) reader(source io.Reader) io.Reader { return source }
