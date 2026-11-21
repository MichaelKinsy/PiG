//go:build !unix

package workers

// holdChannel is a no-op outside Unix: on Windows the worker's IPC channel is a named pipe no grandchild inherits
// (workers_process_end_unix_test.go:18-19), and Pi's fixture holds fd 3 only on Unix. The tests that set HoldsChannel
// (example 76 and exit case 1) still exercise report-and-exit and incarnation tracking with the server's gate released at
// WorkerExitedError; link-closed-before-exit with a held channel is proven at the process level on Unix.
func holdChannel(*channel) error { return nil }
