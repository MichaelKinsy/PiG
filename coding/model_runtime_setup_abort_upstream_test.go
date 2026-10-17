package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A request whose signal is aborted before its provider starts fails inside lazyStream's setup: auth resolution throws the signal's reason (packages/ai/src/auth/resolve.ts:53 signal.throwIfAborted(), raced by raceWithAbortSignal), and packages/ai/src/api/lazy.ts createSetupErrorMessage reports every setup rejection as stopReason "error" with the error's message. A plain abort()'s reason is Node's DOMException AbortError, "This operation was aborted". Pi 1.1.0 shows it after an abort during a tool call: the next turn's request ends with {stopReason:"error", errorMessage:"This operation was aborted"}, never "context canceled".
func TestRequestCancelledBeforeItsProviderStartsIsAPiSetupError(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	called := false
	services.Registry().RegisterExtensionProvider("registered", extension.ProviderConfig{
		API: "registered-api", APIKey: "key", BaseURL: "https://extension.invalid", Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model"}},
		StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
			called = true
			message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ran"}}, StopReason: ai.StopReasonStop}
			return newSessionTestStream(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		},
	})
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
	for _, test := range []struct {
		name            string
		provider, model string
		options         ai.StreamOptions
	}{
		{"registered stream provider", "registered", "model", ai.StreamOptions{}},
		{"built-in provider", "openai", "gpt-4o", ai.StreamOptions{APIKey: "sk-test"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := services.ModelRuntime().GetModel(test.provider, test.model)
			if model == nil {
				t.Fatalf("model %s/%s missing", test.provider, test.model)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			result := services.ModelRuntime().StreamSimple(ctx, model, request, test.options).Result()
			if result.StopReason != ai.StopReasonError || result.ErrorMessage != "This operation was aborted" {
				t.Fatalf("stopReason=%q errorMessage=%q, want %q %q", result.StopReason, result.ErrorMessage, ai.StopReasonError, "This operation was aborted")
			}
			// createSetupErrorMessage lists errorMessage before timestamp; the message keeps that order wherever it is written.
			persisted, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if errorAt, timestampAt := bytes.Index(persisted, []byte(`"errorMessage"`)), bytes.Index(persisted, []byte(`"timestamp"`)); errorAt < 0 || timestampAt < 0 || errorAt > timestampAt {
				t.Fatalf("setup error %s: errorMessage must precede timestamp", persisted)
			}
		})
	}
	if called {
		t.Fatal("the registered provider ran for a cancelled request")
	}

	// A model whose provider object resolves no auth reaches the check after prepare; Pi's lazyStream setup still rejects the request first (resolve.ts:53).
	t.Run("provider without auth resolution", func(t *testing.T) {
		faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "openai", Model: "gpt-4o-mini"})
		faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ran")}, StopReason: "stop"})})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		result := services.ModelRuntime().Complete(ctx, &ai.Model{ID: "gpt-4o-mini", Provider: faux}, request, ai.StreamOptions{})
		if result.StopReason != ai.StopReasonError || result.ErrorMessage != "This operation was aborted" {
			t.Fatalf("stopReason=%q errorMessage=%q, want %q %q", result.StopReason, result.ErrorMessage, ai.StopReasonError, "This operation was aborted")
		}
	})
}

// A caller-owned provider whose id the runtime does not know is Pi's compat path (registerFauxProvider + compat.complete): no Models.getAuth runs, so the
// provider receives the aborted signal and reports stopReason "aborted", and that result in history does not poison the next request (pi-ai
// abort.test.ts:56-99; parity providers-faux-streaming/14-provider-immediate-abort).
func TestCancelledRequestToAnUnregisteredCallerProviderIsAborted(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("ignored")}, StopReason: "stop"})})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
	result := services.ModelRuntime().Complete(ctx, &ai.Model{ID: "faux-1", Provider: faux}, request, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonAborted || len(result.Content) != 0 {
		t.Fatalf("stopReason=%q content=%v errorMessage=%q, want an empty aborted message", result.StopReason, result.Content, result.ErrorMessage)
	}
}

// AbortReason is signal.reason: Node's AbortError for a plain cancellation, still context.Canceled to errors.Is, and a cancellation cause's own error.
func TestAbortReasonIsTheSignalReason(t *testing.T) {
	if err := ai.AbortReason(t.Context()); err != nil {
		t.Fatalf("live context: %v", err)
	}
	plain, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ai.AbortReason(plain); err == nil || err.Error() != "This operation was aborted" || !errors.Is(err, context.Canceled) {
		t.Fatalf("plain cancel: %v", err)
	}
	cause := errors.New("timeout reason")
	withCause, cancelCause := context.WithCancelCause(t.Context())
	cancelCause(cause)
	if err := ai.AbortReason(withCause); !errors.Is(err, cause) || err.Error() != "timeout reason" {
		t.Fatalf("cause: %v", err)
	}
}
