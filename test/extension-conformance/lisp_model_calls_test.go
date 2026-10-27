package extensionconformance

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func closeLispHarness(h *harness) {
	if h.cleanup != nil {
		h.cleanup()
	}
	if h.host != nil {
		h.host.Shutdown("test done")
	}
}

// Context.complete sends the model, the request and the auth to the host and returns the host's answer. The answer is built from all three
// (model id, API key, first message), so an SDK that never makes the call, or sends one of them wrong, cannot produce it.
func TestLispContextCompleteRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Lisp model call conformance in short mode (builds the Lisp fixture)")
	}
	h := makeSubprocessLispHarness(t)
	t.Cleanup(func() { closeLispHarness(h) })
	h.bridge.SetHostAction("complete", func(_ context.Context, model, request, auth map[string]any) (map[string]any, error) {
		messages, _ := request["messages"].([]any)
		first, _ := messages[0].(map[string]any)
		return map[string]any{"text": model["id"].(string) + "/" + auth["apiKey"].(string) + "/" + first["content"].(string)}, nil
	})
	command, ok := findCommand(h.runner, "complete-probe")
	if !ok {
		t.Fatal("complete-probe command not registered")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := command.Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return slices.Contains(h.ui.Recorded(), "complete=m/sk-complete/hello:info") })
}

// A model stream ends with its request: the host cancels the modelStream call and the stream ends with an error event that names the model,
// where an SDK that kept waiting would leave the result unanswered.
func TestLispModelStreamEndsWithItsCancelledRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Lisp model call conformance in short mode (builds the Lisp fixture)")
	}
	h := makeSubprocessLispHarness(t)
	t.Cleanup(func() { closeLispHarness(h) })
	var once sync.Once
	started := make(chan struct{})
	var hostSawCancel atomic.Bool
	h.bridge.SetHostAction("streamModel", func(callCtx context.Context, _, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
		once.Do(func() { close(started) })
		<-callCtx.Done()
		hostSawCancel.Store(true)
		return nil, errors.New("cancelled")
	})
	probe, ok := findCommand(h.runner, "model-stream-cancel-probe")
	report, reportOK := findCommand(h.runner, "model-stream-cancel-report")
	if !ok || !reportOK {
		t.Fatal("model-stream-cancel-probe or model-stream-cancel-report command not registered")
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- probe.Handler(ctx, "") }()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the model stream never reached the host")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the cancelled command did not return")
	}
	waitFor(t, func() bool { return hostSawCancel.Load() })
	waitFor(t, func() bool {
		if err := report.Handler(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		return slices.Contains(h.ui.Recorded(), "cancel=error:hang:info")
	})
}
