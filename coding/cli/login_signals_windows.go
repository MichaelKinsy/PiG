//go:build windows

package cli

import (
	"os"
	"syscall"
)

// dieBySignal ends the process with the status a shell reports for death by sig, 128+signum: Windows has no signal to raise on the process. It does not return.
var dieBySignal = func(sig os.Signal) {
	number, _ := sig.(syscall.Signal)
	os.Exit(128 + int(number))
}
