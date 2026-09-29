//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

// dieBySignal ends the process with sig's default action, as Node does for a
// signal without a listener: the parent sees termination by that signal.
func dieBySignal(sig os.Signal) {
	number, ok := sig.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}
	signal.Reset(sig)
	_ = syscall.Kill(os.Getpid(), number)
	select {}
}
