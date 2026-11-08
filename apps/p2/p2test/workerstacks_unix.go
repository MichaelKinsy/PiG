//go:build !windows

package p2test

import (
	"os"
	"os/signal"
	"runtime/pprof"
	"syscall"
)

// stacksEnd is the line a worker writes to stderr after the stacks of its goroutines: the report waits for it.
const stacksEnd = "p2test: the stacks of the goroutines end"

// dumpStacksOnSignal makes the worker (the test binary run as `p2 worker`) write every goroutine's stack to stderr when it
// gets SIGUSR1, and go on: a worker that answers nothing is killed by its server at the heartbeat, and its stacks are the
// only account of why. The dump is the user-level one of a panic's (`debug=2`), without the runtime's frames.
func dumpStacksOnSignal() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	//portlint:allow golifetime the worker process's own signal loop, which ends with the process
	go func() {
		for range signals {
			_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
			_, _ = os.Stderr.WriteString(stacksEnd + "\n")
		}
	}()
}

// askForStacks asks the process pid for its stacks; false when it cannot be signalled.
func askForStacks(pid int) bool {
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.SIGUSR1) == nil
}
