//go:build !windows

package cli

import (
	"os"
	"syscall"
	"testing"
)

// The first signal cancels the command's context and is recorded; a second one ends the process at once, without the cleanup the first started.
func TestCommandSignalsCancelOnTheFirstAndEndOnTheSecond(t *testing.T) {
	died := make(chan os.Signal, 1)
	original := dieBySignal
	dieBySignal = func(sig os.Signal) { died <- sig }
	t.Cleanup(func() { dieBySignal = original })

	signals := notifyCommandSignals(t.Context(), syscall.SIGUSR1)
	defer signals.Stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	<-signals.ctx.Done()
	if sig, fired := signals.Fired(); !fired || sig != syscall.SIGUSR1 {
		t.Fatalf("Fired = %v, %v; want SIGUSR1", sig, fired)
	}
	select {
	case sig := <-died:
		t.Fatalf("the first signal ended the process by %v", sig)
	default:
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	if sig := <-died; sig != syscall.SIGUSR1 {
		t.Fatalf("the second signal ended the process by %v", sig)
	}
}
