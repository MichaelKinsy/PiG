package coding

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream awaits extension dispatch for each agent event in order, so the
// same message's message_end handler cannot overtake message_start.
func TestMessageEndHandlerWaitsForMessageStartHandler(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	endRan := make(chan struct{})
	var startOnce, endOnce sync.Once
	var mu sync.Mutex
	var order []string
	ext := extension.Extension{Path: "order", Handlers: map[string][]extension.HandlerFn{
		"message_start": {func(...any) (any, error) {
			blocked := false
			startOnce.Do(func() {
				blocked = true
				close(startEntered)
			})
			if blocked {
				<-releaseStart
			}
			mu.Lock()
			order = append(order, "start")
			mu.Unlock()
			return nil, nil
		}},
		"message_end": {func(...any) (any, error) {
			mu.Lock()
			order = append(order, "end")
			mu.Unlock()
			endOnce.Do(func() { close(endRan) })
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext}, fauxReply("ok", ai.StopReasonStop, 0))
	done := make(chan error, 1)
	go func() {
		_, err := h.session.Send(context.Background(), "run")
		done <- err
	}()
	<-startEntered
	select {
	case <-endRan:
		t.Fatal("message_end handler overtook blocked message_start handler")
	default:
	}
	close(releaseStart)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.settle(t)
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 2 || order[0] != "start" || order[1] != "end" {
		t.Fatalf("extension handler order = %v", order)
	}
}
