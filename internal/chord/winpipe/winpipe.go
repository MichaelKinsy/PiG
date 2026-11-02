// Package winpipe is the named-pipe listener the Chord host's Windows channels use (the plugins' Node subprocess, a worker's
// IPC channel, a worker's process lock): go-winio's, whose Close always returns.
//
// go-winio v0.6.2's Close hands its request to the goroutine that accepts and waits for that goroutine to end. When the
// request reaches the goroutine in the middle of a connect it aborts, the connect can answer with ERROR_OPERATION_ABORTED
// instead of the error go-winio maps to a closed listener; the goroutine then goes on as if nothing was asked, the
// request is lost, and Close waits for a goroutine that waits for a request. A second request reaches it, so Close here
// repeats its request until the goroutine ends. The package has code only on Windows.
package winpipe
