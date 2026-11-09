package app

import (
	"sync"
	"testing"
	"time"
)

type recordingSender struct {
	mu    sync.Mutex
	calls []bool
	stops int
}

func (s *recordingSender) Send(partial bool) { s.mu.Lock(); s.calls = append(s.calls, partial); s.mu.Unlock() }
func (s *recordingSender) Stop()             { s.mu.Lock(); s.stops++; s.mu.Unlock() }

// Pi worker.ts: the close is Promise.race([send(true), sleep(1 s)]); the final send queues behind a periodic send in flight and the
// one bound covers both.
func TestWorkerCloseBoundCoversAPeriodicSendStillInFlight(t *testing.T) {
	ticked := make(chan struct{}) // a periodic send that never ends
	sender := &recordingSender{}
	start := time.Now()
	closeTelemetry(sender, ticked, 50*time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("close took %v past its bound", took)
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.stops != 1 || len(sender.calls) != 0 {
		t.Fatalf("stops=%d calls=%v: the final send must wait behind the periodic one", sender.stops, sender.calls)
	}
}

func TestWorkerCloseSendsTheFinalPartialSendOnceThePeriodicOneEnded(t *testing.T) {
	ticked := make(chan struct{})
	close(ticked)
	sender := &recordingSender{}
	closeTelemetry(sender, ticked, time.Second)
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.calls) != 1 || !sender.calls[0] || sender.stops != 1 {
		t.Fatalf("calls=%v stops=%d", sender.calls, sender.stops)
	}
}
