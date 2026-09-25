package coding

import (
	"context"
	"testing"
)

// A run's owned cleanup must leave a newer run's cancellation in place, so
// Abort and Close still reach it (upstream Agent finishes activeRun before
// another prompt can claim it).
func TestPreviousRunCleanupPreservesNextRun(t *testing.T) {
	s := &Session{closeDone: make(chan struct{})}
	_, endOld := s.beginAgentRun(context.Background())
	next, endNext := s.beginAgentRun(context.Background())
	endOld()

	waited := make(chan error, 1)
	go func() { waited <- s.WaitForIdle(t.Context()) }()
	s.RequestAbort()
	if next.Err() != context.Canceled {
		t.Fatalf("the earlier run's cleanup erased the next run: next.Err=%v", next.Err())
	}
	if !s.IsStreaming() {
		t.Fatal("abort marked the run idle before its owner ended it")
	}
	endNext()
	if err := <-waited; err != nil {
		t.Fatalf("WaitForIdle: %v", err)
	}
	if s.IsStreaming() {
		t.Fatal("run state left behind after the last run ended")
	}
}
