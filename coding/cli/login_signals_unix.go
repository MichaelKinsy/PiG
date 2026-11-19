//go:build !windows

package cli

import (
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// dieBySignal ends the process by sig: the default action is restored and the signal raised on the process, so a parent sees death by that signal, as it does
// from a Node process that no handler of its own has caught. It does not return.
var dieBySignal = func(sig os.Signal) {
	number, ok := sig.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}
	signal.Reset(number)
	_ = syscall.Kill(os.Getpid(), number)
	// The default action ends the process; a sleeping goroutine keeps the runtime from reporting a deadlock before it does.
	for {
		time.Sleep(time.Duration(math.MaxInt64))
	}
}
