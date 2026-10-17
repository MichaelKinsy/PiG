package extensionconformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// bridgePtr is bridgeResult for the events whose runner method reads a *Result.
func bridgePtr[E, R any](handlers []func(context.Context, E) (R, error)) []extension.HandlerFn {
	out := make([]extension.HandlerFn, 0, len(handlers))
	for _, handler := range handlers {
		out = append(out, func(args ...any) (any, error) {
			result, err := handler(args[1].(context.Context), args[0].(E))
			return &result, err
		})
	}
	return out
}

func apiReferenceRunner(t *testing.T, handlers map[string][]extension.HandlerFn) *inproc.Runner {
	t.Helper()
	return inproc.NewRunner([]extension.Extension{{Path: "api-reference", Handlers: handlers}}, t.TempDir())
}

// TestConformanceAPIOnToolCallBlockEndsDispatch is the Go reference for pi.on("tool_call") (extensions/types.ts:1625): a handler registered through extension.API.OnToolCall that returns block ends the dispatch with its reason, so a later handler never runs (runner.ts emitToolCall returns on block).
//
// mutation-checked: the mutant "Runner.EmitToolCall keeps running handlers after a block result" fails it.
func TestConformanceAPIOnToolCallBlockEndsDispatch(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var ran []string
	api.OnToolCall(func(context.Context, extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
		ran = append(ran, "first")
		return extension.ToolCallEventResult{Block: true, Reason: "denied"}, nil
	})
	api.OnToolCall(func(context.Context, extension.ToolCallEvent) (extension.ToolCallEventResult, error) {
		ran = append(ran, "second")
		return extension.ToolCallEventResult{}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"tool_call": bridgePtr(fake.OnToolCallHandlers)})
	event := extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "x", Input: map[string]any{}}
	result, err := runner.EmitToolCall(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Block || result.Reason != "denied" || !reflect.DeepEqual(ran, []string{"first"}) {
		t.Fatalf("result %+v, handlers that ran %v: want the first handler's block and no second handler", result, ran)
	}
}

// TestConformanceAPIOnToolResultChainsModifications is the Go reference for pi.on("tool_result") (extensions/types.ts:1631, runner.ts:707-755 emitToolResult): a handler registered through extension.API.OnToolResult sees the details the previous handler returned, and the emit result carries the last modification.
//
// mutation-checked: the mutant "Runner.EmitToolResult hands every handler the original event" fails it.
func TestConformanceAPIOnToolResultChainsModifications(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var seenBySecond any
	api.OnToolResult(func(context.Context, extension.ToolResultEvent) (extension.ToolResultEventResult, error) {
		return extension.ToolResultEventResult{Details: "one"}, nil
	})
	api.OnToolResult(func(_ context.Context, evt extension.ToolResultEvent) (extension.ToolResultEventResult, error) {
		seenBySecond = evt.(extension.CustomToolResultEvent).Details
		return extension.ToolResultEventResult{Details: "two"}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"tool_result": bridgePtr(fake.OnToolResultHandlers)})
	event := extension.CustomToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1", Input: map[string]any{}}, ToolName: "x", Details: "original"}
	result, err := runner.EmitToolResult(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if seenBySecond != "one" || result == nil || result.Details != "two" {
		t.Fatalf("second handler saw %v, result %+v: want it to see \"one\" and the result to carry \"two\"", seenBySecond, result)
	}
}

// TestConformanceAPIOnInputTransformsChain is the Go reference for pi.on("input") (extensions/types.ts:1633, runner.ts emitInput): a handler registered through extension.API.OnInput that returns transform feeds its text to the next handler, and the emit result is that transform.
//
// mutation-checked: the mutant "Runner.EmitInput hands every handler the original text" fails it.
func TestConformanceAPIOnInputTransformsChain(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var seenBySecond string
	api.OnInput(func(context.Context, extension.InputEvent) (extension.InputEventResult, error) {
		return extension.InputEventResultTransform{Text: "transformed"}, nil
	})
	api.OnInput(func(_ context.Context, evt extension.InputEvent) (extension.InputEventResult, error) {
		seenBySecond = evt.Text
		return extension.InputEventResultContinue{}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"input": bridgeResult(fake.OnInputHandlers)})
	result, err := runner.EmitInput(t.Context(), "raw", nil, "interactive", "")
	if err != nil {
		t.Fatal(err)
	}
	transform, ok := result.(extension.InputEventResultTransform)
	if seenBySecond != "transformed" || !ok || transform.Text != "transformed" {
		t.Fatalf("second handler saw %q, result %#v: want the transform to chain", seenBySecond, result)
	}
}

// TestConformanceAPIOnMessageAndContextEventsChain is the Go reference for pi.on("message_end" | "context" | "context_with_system" | "before_provider_headers") (extensions/types.ts:1596-1612): a handler registered through extension.API.On<Event> sees what the previous handler produced, and the emitter returns the last replacement.
//
// mutation-checked: the mutant "Runner.EmitContext hands every handler the original messages" fails it.
func TestConformanceAPIOnMessageAndContextEventsChain(t *testing.T) {
	t.Parallel()
	original := extension.AgentMessage{Custom: map[string]any{"role": "user", "content": "a"}}
	replaced := extension.AgentMessage{Custom: map[string]any{"role": "user", "content": "b"}}

	t.Run("message_end", func(t *testing.T) {
		t.Parallel()
		fake := &extensiontest.Fake{}
		var api extension.API = fake
		var seen extension.AgentMessage
		api.OnMessageEnd(func(context.Context, extension.MessageEndEvent) (extension.MessageEndEventResult, error) {
			message := replaced
			return extension.MessageEndEventResult{Message: &message}, nil
		})
		api.OnMessageEnd(func(_ context.Context, evt extension.MessageEndEvent) (extension.MessageEndEventResult, error) {
			seen = evt.Message
			return extension.MessageEndEventResult{}, nil
		})
		runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"message_end": bridgePtr(fake.OnMessageEndHandlers)})
		got, err := runner.EmitMessageEnd(t.Context(), extension.MessageEndEvent{Type: "message_end", Message: original})
		if err != nil || !reflect.DeepEqual(seen, replaced) || got == nil || !reflect.DeepEqual(*got, replaced) {
			t.Fatalf("second handler saw %v, emit returned %v (%v): want the replacement from the first", seen, got, err)
		}
	})
	t.Run("context", func(t *testing.T) {
		t.Parallel()
		fake := &extensiontest.Fake{}
		var api extension.API = fake
		var seen []extension.AgentMessage
		api.OnContext(func(context.Context, extension.ContextEvent) (extension.ContextEventResult, error) {
			return extension.ContextEventResult{Messages: []extension.AgentMessage{replaced}}, nil
		})
		api.OnContext(func(_ context.Context, evt extension.ContextEvent) (extension.ContextEventResult, error) {
			seen = evt.Messages
			return extension.ContextEventResult{}, nil
		})
		runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"context": bridgePtr(fake.OnContextHandlers)})
		got, err := runner.EmitContext(t.Context(), []extension.AgentMessage{original})
		want := []extension.AgentMessage{replaced}
		if err != nil || !reflect.DeepEqual(seen, want) || !reflect.DeepEqual(got, want) {
			t.Fatalf("second handler saw %v, emit returned %v (%v): want the replacement from the first", seen, got, err)
		}
	})
	t.Run("context_with_system", func(t *testing.T) {
		t.Parallel()
		fake := &extensiontest.Fake{}
		var api extension.API = fake
		var seen []extension.AgentMessage
		api.OnContextWithSystem(func(context.Context, extension.ContextWithSystemEvent) (extension.ContextEventResult, error) {
			return extension.ContextEventResult{Messages: []extension.AgentMessage{replaced}}, nil
		})
		api.OnContextWithSystem(func(_ context.Context, evt extension.ContextWithSystemEvent) (extension.ContextEventResult, error) {
			seen = evt.Messages
			return extension.ContextEventResult{}, nil
		})
		runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"context_with_system": bridgePtr(fake.OnContextWithSystemHandlers)})
		got, err := runner.EmitContextWithSystem(t.Context(), []extension.AgentMessage{original})
		want := []extension.AgentMessage{replaced}
		if err != nil || !reflect.DeepEqual(seen, want) || !reflect.DeepEqual(got, want) {
			t.Fatalf("second handler saw %v, emit returned %v (%v): want the replacement from the first", seen, got, err)
		}
	})
	t.Run("before_provider_headers", func(t *testing.T) {
		t.Parallel()
		fake := &extensiontest.Fake{}
		var api extension.API = fake
		value := "1"
		var seen *string
		api.OnBeforeProviderHeaders(func(_ context.Context, evt extension.BeforeProviderHeadersEvent) error {
			evt.Headers["x-a"] = &value
			return nil
		})
		api.OnBeforeProviderHeaders(func(_ context.Context, evt extension.BeforeProviderHeadersEvent) error {
			seen = evt.Headers["x-a"]
			return nil
		})
		runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"before_provider_headers": bridgeVoid(fake.OnBeforeProviderHeadersHandlers)})
		got, err := runner.EmitBeforeProviderHeaders(t.Context(), extension.ProviderHeaders{})
		if err != nil || seen == nil || *seen != "1" || got["x-a"] == nil || *got["x-a"] != "1" {
			t.Fatalf("second handler saw %v, emit returned %v (%v): want the header the first set", seen, got, err)
		}
	})
}
