//go:build windows

package extension

import "os"

// terminate mirrors upstream's proc.kill("SIGTERM"), which Node implements on
// Windows with libuv's uv_process_kill: TerminateProcess ends the child alone
// with status 1. Once that succeeds, libuv records the signal as the child's
// exit signal (src/win/process.c uv_process_kill sets exit_signal), so Node's
// exit event carries code null and signal "SIGTERM". terminate reports that
// the child then has no exit code. A child that had already exited is not
// terminated and keeps its own code, as in libuv.
func terminate(process *os.Process) (noExitCode bool) {
	return process.Kill() == nil
}
