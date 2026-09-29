package execution_test

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/ai"
)

func assistantMessage(text string, reason ai.StopReason) *ai.AssistantMessage {
	message := &ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: "test", Provider: "provider", Model: "model",
		Usage: ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10}, StopReason: reason, Timestamp: 2,
	}
	if reason == ai.StopReasonError {
		message.ErrorMessage = text
	}
	return message
}

func requestModel() *ai.Model {
	return &ai.Model{ID: "model", DisplayName: "Model", ProviderMeta: ai.ProviderMetadata{API: "test", ProviderID: "provider", BaseURL: "https://example.invalid", Reasoning: true},
		Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384},
	}
}

func toProviderMessages(_ harness.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
	filtered := make([]agent.AgentMessage, 0, len(messages))
	for _, message := range messages {
		switch message.Role() {
		case "user", "assistant", "toolResult":
			filtered = append(filtered, message)
		}
	}
	return harness.ConvertToLlm(filtered), nil
}

func providerUser(text string) ai.Message {
	return ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}
}

func assistantObserver() execution.AssistantStreamObserver {
	return execution.AssistantStreamObserver{
		Start:  func(harness.Context, *ai.AssistantMessage, ai.StartEvent) error { return nil },
		Update: func(harness.Context, *ai.AssistantMessage, ai.AssistantMessageEvent) error { return nil },
		End:    func(harness.Context, *ai.AssistantMessage) error { return nil },
	}
}

func assistantStream(t *testing.T, events ...ai.AssistantMessageEvent) *ai.AssistantMessageEventStream {
	t.Helper()
	stream := ai.NewAssistantMessageEventStream()
	// Upstream queues this synchronous event batch before the awaited request resumes. Push queues events without invoking the observer.
	for _, event := range events {
		requireNoError(t, stream.Push(event))
	}
	return stream
}

func TestPortWave01ExecutionAssistant(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/execution-assistant.test.ts:75
	t.Run("maps curated options and runs the assistant lifecycle without mutating input", func(t *testing.T) {
		input := []agent.AgentMessage{userMessage("original", 1)}
		model := requestModel()
		controller, cancel := harness.WithCancel(harness.BackgroundContext())
		defer cancel(nil)
		order := []string{}
		starts, updates, ends := []*ai.AssistantMessage{}, []*ai.AssistantMessage{}, []*ai.AssistantMessage{}
		transformedCopy := false
		var converted []agent.AgentMessage
		var payload any
		var receivedContext ai.Context
		var receivedOptions ai.StreamOptions
		var metadata execution.AssistantResponseMetadata
		var startEventType ai.AssistantEventType
		result, err := execution.StreamHarnessAssistant(harness.WithAbortSignal(harness.BackgroundContext(), controller), input, execution.HarnessAssistantStreamConfig{
			Model: model, SystemPrompt: "system", ThinkingLevel: ai.ThinkingHigh,
			StreamOptions: harness.AgentHarnessStreamOptions{
				Transport: ai.Transport("websocket"), TimeoutMs: new(123), MaxRetries: new(2), MaxRetryDelayMs: new(456),
				Headers: map[string]string{"authorization": "test"}, Metadata: map[string]any{"tenant": "one"}, CacheRetention: "long", Deferred: &harness.AgentHarnessDeferredOption{Object: true, Window: "1h"},
			},
			TransformContext: func(_ harness.Context, ctx execution.AssistantRequestContext) (execution.AssistantRequestContext, error) {
				order = append(order, "transform_context")
				transformedCopy = len(ctx.Messages) != 0 && &ctx.Messages[0] != &input[0]
				ctx.Messages = append(ctx.Messages, userMessage("injected", 1))
				return execution.AssistantRequestContext{Messages: ctx.Messages, SystemPrompt: "transformed system"}, nil
			},
			ToProviderMessages: func(ctx harness.Context, messages []agent.AgentMessage) ([]ai.Message, error) {
				order = append(order, "to_provider_messages")
				converted = messages
				return toProviderMessages(ctx, messages)
			},
			BeforePayload: func(_ harness.Context, original any, seenModel *ai.Model) (any, error) {
				order = append(order, "before_payload")
				requireEqual(t, seenModel.ID, "resolved")
				payload = original
				return map[string]any{"replaced": true}, nil
			},
			AfterResponse: func(_ harness.Context, message *ai.AssistantMessage, response execution.AssistantResponseMetadata) (*ai.AssistantMessage, error) {
				order = append(order, "after_response")
				metadata = response
				transformed := *message
				transformed.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "transformed"}}
				return &transformed, nil
			},
			Request: func(ctx harness.Context, request ai.Context, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				order = append(order, "request")
				receivedContext, receivedOptions = request, options
				resolved := *model
				resolved.ID = "resolved"
				if options.OnPayload == nil || options.OnResponse == nil {
					t.Fatal("missing request lifecycle callbacks")
				}
				replacement, err := options.OnPayload(map[string]any{"original": true}, &resolved)
				requireNoError(t, err)
				requireEqual(t, replacement, map[string]any{"replaced": true})
				requireNoError(t, options.OnResponse(ctx, ai.ProviderResponse{Status: 201, Headers: map[string]string{"request-id": "r1"}}, model))
				initial := assistantMessage("", ai.StopReasonPending)
				partial := *initial
				partial.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "raw"}}
				return assistantStream(t, ai.StartEvent{Partial: initial}, ai.TextDeltaEvent{ContentIndex: 0, Delta: "raw", Partial: &partial}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: assistantMessage("raw", ai.StopReasonStop)}), nil
			},
			Observer: execution.AssistantStreamObserver{
				Start: func(_ harness.Context, message *ai.AssistantMessage, event ai.StartEvent) error {
					order = append(order, "observer_start")
					starts = append(starts, message)
					startEventType = event.EventType()
					return nil
				},
				Update: func(_ harness.Context, message *ai.AssistantMessage, _ ai.AssistantMessageEvent) error {
					order = append(order, "observer_update")
					updates = append(updates, message)
					return nil
				},
				End: func(_ harness.Context, message *ai.AssistantMessage) error {
					order = append(order, "observer_end")
					ends = append(ends, message)
					return nil
				},
			},
		})
		requireNoError(t, err)
		requireEqual(t, input, []agent.AgentMessage{userMessage("original", 1)})
		requireEqual(t, transformedCopy, true)
		requireEqual(t, converted, []agent.AgentMessage{userMessage("original", 1), userMessage("injected", 1)})
		requireEqual(t, receivedContext.SystemPrompt, "transformed system")
		requireEqual(t, receivedContext.Messages, []ai.Message{providerUser("original"), providerUser("injected")})
		requireEqual(t, payload, map[string]any{"original": true})
		requireEqual(t, receivedOptions.Transport, ai.Transport("websocket"))
		requireEqual(t, receivedOptions.TimeoutMs, new(123))
		requireEqual(t, receivedOptions.MaxRetries, new(2))
		requireEqual(t, receivedOptions.MaxRetryDelayMs, new(456))
		requireEqual(t, receivedOptions.Headers, ai.ProviderHeaders{"authorization": new("test")})
		requireEqual(t, receivedOptions.Metadata, map[string]any{"tenant": "one"})
		requireEqual(t, receivedOptions.CacheRetention, ai.CacheRetention("long"))
		requireEqual(t, receivedOptions.Deferred, &ai.DeferredOption{Object: true, Window: "1h"})
		requireEqual(t, receivedOptions.Thinking, ai.ThinkingHigh)
		if receivedOptions.Signal == nil || receivedOptions.Signal.Done() != controller.Done() {
			t.Error("request did not retain the invocation abort signal identity")
		}
		if receivedOptions.TelemetryContext != harness.NoopTelemetryContext {
			t.Error("request telemetry is not the shared no-op parent")
		}
		requireEqual(t, metadata, execution.AssistantResponseMetadata{Status: new(201), Headers: map[string]string{"request-id": "r1"}})
		requireEqual(t, len(starts), 1)
		requireEqual(t, startEventType, ai.EventStart)
		requireEqual(t, len(updates), 1)
		if starts[0] == updates[0] {
			t.Error("start and update reused a message object")
		}
		if len(ends) == 0 || ends[0] != result {
			t.Fatal("observer end did not receive the final result object")
		}
		requireEqual(t, result.Content, []ai.AssistantContentBlock{ai.TextContent{Text: "transformed"}})
		requireEqual(t, order, []string{"transform_context", "to_provider_messages", "request", "before_payload", "observer_start", "observer_update", "after_response", "observer_end"})
	})
	// upstream: packages/agent/test/harness/execution-assistant.test.ts:207
	t.Run("runs against the faux provider request boundary", func(t *testing.T) {
		faux := ai.NewFauxProvider(ai.FauxConfig{MinTokenSize: 1, MaxTokenSize: 1})
		t.Cleanup(func() { requireNoError(t, faux.Close()) })
		faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("hello")}, StopReason: "stop"})})
		lifecycle := []string{}
		var seen []ai.Message
		result, err := execution.StreamHarnessAssistant(harness.BackgroundContext(), []agent.AgentMessage{userMessage("prompt", 1)}, execution.HarnessAssistantStreamConfig{
			Model: faux.GetModel(), SystemPrompt: "system", ThinkingLevel: ai.ThinkingOff, StreamOptions: harness.AgentHarnessStreamOptions{}, ToProviderMessages: toProviderMessages,
			Request: func(ctx harness.Context, request ai.Context, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				seen = request.Messages
				return faux.Stream(ctx, ai.NormalizeContext(request), options)
			},
			Observer: execution.AssistantStreamObserver{
				Start: func(harness.Context, *ai.AssistantMessage, ai.StartEvent) error {
					lifecycle = append(lifecycle, "start")
					return nil
				},
				Update: func(harness.Context, *ai.AssistantMessage, ai.AssistantMessageEvent) error {
					lifecycle = append(lifecycle, "update")
					return nil
				},
				End: func(harness.Context, *ai.AssistantMessage) error { lifecycle = append(lifecycle, "end"); return nil },
			},
		})
		requireNoError(t, err)
		requireEqual(t, seen, []ai.Message{providerUser("prompt")})
		requireEqual(t, result.Content, []ai.AssistantContentBlock{ai.TextContent{Text: "hello"}})
		if len(lifecycle) == 0 {
			t.Fatal("missing lifecycle")
		}
		requireEqual(t, lifecycle[0], "start")
		requireEqual(t, lifecycle[len(lifecycle)-1], "end")
		updates := 0
		for _, event := range lifecycle {
			if event == "update" {
				updates++
			}
		}
		if updates == 0 {
			t.Fatal("faux stream produced no updates")
		}
	})
	// upstream: packages/agent/test/harness/execution-assistant.test.ts:247
	t.Run("rejects a successful terminal event before start", func(t *testing.T) {
		final := assistantMessage("complete", ai.StopReasonStop)
		var options *ai.StreamOptions
		_, err := execution.StreamHarnessAssistant(harness.BackgroundContext(), []agent.AgentMessage{userMessage("prompt", 1)}, execution.HarnessAssistantStreamConfig{
			Model: requestModel(), SystemPrompt: "system", ThinkingLevel: ai.ThinkingOff, StreamOptions: harness.AgentHarnessStreamOptions{}, ToProviderMessages: toProviderMessages,
			Request: func(_ harness.Context, _ ai.Context, requestOptions ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				options = &requestOptions
				return assistantStream(t, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}), nil
			}, Observer: assistantObserver(),
		})
		if err == nil || !strings.Contains(err.Error(), "done before start") {
			t.Fatalf("error = %v, want done before start", err)
		}
		if options == nil {
			t.Fatal("request did not run")
		}
		requireEqual(t, options.Thinking, ai.ThinkingLevel(""))
	})
	// upstream: packages/agent/test/harness/execution-assistant.test.ts:273
	t.Run("keeps the raw settlement when cancellation interrupts after_response", func(t *testing.T) {
		final := assistantMessage("raw", ai.StopReasonStop)
		ended := []*ai.AssistantMessage{}
		observer := assistantObserver()
		observer.End = func(_ harness.Context, message *ai.AssistantMessage) error {
			ended = append(ended, message)
			return nil
		}
		result, err := execution.StreamHarnessAssistant(harness.BackgroundContext(), []agent.AgentMessage{userMessage("prompt", 1)}, execution.HarnessAssistantStreamConfig{
			Model: requestModel(), SystemPrompt: "system", ThinkingLevel: ai.ThinkingOff, StreamOptions: harness.AgentHarnessStreamOptions{}, ToProviderMessages: toProviderMessages,
			Request: func(harness.Context, ai.Context, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				initial := *final
				initial.Content = []ai.AssistantContentBlock{}
				initial.StopReason = ai.StopReasonPending
				return assistantStream(t, ai.StartEvent{Partial: &initial}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}), nil
			},
			AfterResponse: func(harness.Context, *ai.AssistantMessage, execution.AssistantResponseMetadata) (*ai.AssistantMessage, error) {
				return nil, &execution.AbortRequested{Cancellation: func() error { return nil }}
			},
			Observer: observer,
		})
		requireNoError(t, err)
		if result != final {
			t.Fatal("cancellation replaced the raw settlement")
		}
		requireEqual(t, ended, []*ai.AssistantMessage{final})
	})
	// upstream: packages/agent/test/harness/execution-assistant.test.ts:310
	t.Run("returns provider error settlements through the same lifecycle", func(t *testing.T) {
		final := assistantMessage("provider failed", ai.StopReasonError)
		events := []string{}
		result, err := execution.StreamHarnessAssistant(harness.BackgroundContext(), []agent.AgentMessage{userMessage("prompt", 1)}, execution.HarnessAssistantStreamConfig{
			Model: requestModel(), SystemPrompt: "system", ThinkingLevel: ai.ThinkingOff, StreamOptions: harness.AgentHarnessStreamOptions{}, ToProviderMessages: toProviderMessages,
			Request: func(harness.Context, ai.Context, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return assistantStream(t, ai.ErrorEvent{Reason: ai.StopReasonError, Error: final}), nil
			},
			Observer: execution.AssistantStreamObserver{
				Start: func(harness.Context, *ai.AssistantMessage, ai.StartEvent) error {
					t.Error("pre-generation error must not synthesize start")
					return nil
				},
				Update: func(harness.Context, *ai.AssistantMessage, ai.AssistantMessageEvent) error {
					events = append(events, "update")
					return nil
				},
				End: func(_ harness.Context, message *ai.AssistantMessage) error {
					events = append(events, "end:"+string(message.StopReason))
					return nil
				},
			},
		})
		requireNoError(t, err)
		if result != final {
			t.Fatal("provider error settlement identity changed")
		}
		requireEqual(t, events, []string{"end:error"})
	})
}
