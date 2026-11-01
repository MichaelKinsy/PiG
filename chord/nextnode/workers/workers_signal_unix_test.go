//go:build unix

package workers

import (
	"syscall"
	"testing"
)

// Ports the SIGTERM half of main.test.ts "shuts down on SIGTERM and on the parent's disconnect".
func TestTheWorkersSideShutsDownOnSIGTERM(t *testing.T) {
	term := started(t, workerBehaviour{})
	until(t, func() bool { return term.has(FromWorkerReady) })
	if err := syscall.Kill(term.child.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return term.exited() != nil })
	if exit := term.exited(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v, want {code: 0, signal: null}", exit)
	}
}
