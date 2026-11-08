package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi api/lazy.ts:83 (f3a64af07): a rejected setup ends as "aborted" when the request signal is aborted, whatever caused the rejection.
func TestModelRuntimeRejectedPreparationIsAbortedWhenCanceled(t *testing.T) {
	runtime := newRuntimeTestServices(t).ModelRuntime()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runtime.prepare = func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		cancel()
		return nil, nil, ai.StreamOptions{}, ctx.Err()
	}
	stream := runtime.Stream(ctx, &ai.Model{ID: "model"}, ai.Context{}, ai.StreamOptions{})
	events := collectRuntimeEvents(t.Context(), stream)
	if len(events) != 1 {
		t.Fatalf("events=%#v", events)
	}
	terminal, ok := events[0].(ai.ErrorEvent)
	if !ok || terminal.Reason != ai.StopReasonAborted || terminal.Error.StopReason != ai.StopReasonAborted || terminal.Error != stream.Result() {
		t.Fatalf("terminal=%#v", events[0])
	}
}

func TestModelRuntimeProviderTimeoutWithoutRequestCancellationIsError(t *testing.T) {
	runtime := newRuntimeTestServices(t).ModelRuntime()
	runtime.prepare = func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		return nil, nil, ai.StreamOptions{}, context.DeadlineExceeded
	}
	response := runtime.Complete(t.Context(), &ai.Model{ID: "model"}, ai.Context{}, ai.StreamOptions{})
	if response.StopReason != ai.StopReasonError {
		t.Fatalf("uncanceled request=%#v", response)
	}
}
