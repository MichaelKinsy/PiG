package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi api/lazy.ts:46-59 keeps a rejected setup as an error, even if cancellation caused that rejection.
func TestModelRuntimeRejectedPreparationStaysErrorWhenCanceled(t *testing.T) {
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
	if !ok || terminal.Reason != ai.StopReasonError || terminal.Error.StopReason != ai.StopReasonError || terminal.Error != stream.Result() {
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
