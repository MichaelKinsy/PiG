//go:build unix

package workers

import (
	"syscall"
	"testing"
)

// pi: packages/chord/src/node/workers/main.ts
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

// An OnError that throws while the closers run rejects Pi's unawaited `#shutdown`: `unhandledRejection`, fatal (main.ts:100).
func TestTheWorkersSideTreatsAnOnErrorPanicInTheClosersAsFatal(t *testing.T) {
	worker := started(t, workerBehaviour{CloserPanics: true})
	until(t, func() bool { return worker.has(FromWorkerReady) })
	if err := syscall.Kill(worker.child.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return worker.exited() != nil })
	if exit := worker.exited(); exit.Code == nil || *exit.Code != 1 {
		t.Fatalf("exit = %+v, want code 1", exit)
	}
	if !worker.has(FromWorkerFatal) {
		t.Fatalf("control = %+v, want a fatal", worker.messages())
	}
}
