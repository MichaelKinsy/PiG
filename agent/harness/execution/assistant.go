package execution

// Ports packages/agent/src/harness/execution/assistant.ts.

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/ai"
)

// AssistantResponseMetadata is captured before the response body is consumed.
type AssistantResponseMetadata struct {
	Status  *int
	Headers map[string]string
}

// AssistantStreamObserver observes one stream's ordered lifecycle. Each callback completes before the next phase starts.
type AssistantStreamObserver struct {
	Start  func(harness.Context, *ai.AssistantMessage, ai.StartEvent) error
	Update func(harness.Context, *ai.AssistantMessage, ai.AssistantMessageEvent) error
	End    func(harness.Context, *ai.AssistantMessage) error
}

// AssistantRequestContext carries the mutable copy supplied to transform_context.
type AssistantRequestContext struct {
	Messages     []agent.AgentMessage
	SystemPrompt string
}

// HarnessAssistantStreamConfig supplies one already-approved provider request.
type HarnessAssistantStreamConfig struct {
	Model              *ai.Model
	SystemPrompt       string
	Tools              []ai.ToolSchema
	ThinkingLevel      ai.ThinkingLevel
	StreamOptions      harness.AgentHarnessStreamOptions
	TransformContext   func(harness.Context, AssistantRequestContext) (AssistantRequestContext, error)
	ToProviderMessages func(harness.Context, []agent.AgentMessage) ([]ai.Message, error)
	BeforePayload      func(harness.Context, any, *ai.Model) (any, error)
	AfterResponse      func(harness.Context, *ai.AssistantMessage, AssistantResponseMetadata) (*ai.AssistantMessage, error)
	Request            func(harness.Context, ai.Context, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	Observer           AssistantStreamObserver
}

func createRequestOptions(ctx harness.Context, config HarnessAssistantStreamConfig, capture func(AssistantResponseMetadata)) ai.StreamOptions {
	options := config.StreamOptions
	request := ai.StreamOptions{
		Transport: options.Transport, TimeoutMs: options.TimeoutMs, MaxRetries: options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs, Metadata: options.Metadata,
		CacheRetention: ai.CacheRetention(options.CacheRetention), Deferred: options.Deferred,
		Signal: ctx, TelemetryContext: harness.GetTelemetryContext(ctx),
	}
	if options.Headers != nil {
		request.Headers = make(ai.ProviderHeaders, len(options.Headers))
		for name, value := range options.Headers {
			request.Headers[name] = new(value)
		}
	}
	if config.ThinkingLevel != ai.ThinkingOff {
		request.Thinking = config.ThinkingLevel
	}
	if config.BeforePayload != nil {
		request.OnPayload = func(payload any, model *ai.Model) (any, error) { return config.BeforePayload(ctx, payload, model) }
	}
	request.OnResponse = func(_ context.Context, response ai.ProviderResponse, _ *ai.Model) error {
		capture(AssistantResponseMetadata{Status: new(response.Status), Headers: response.Headers})
		return nil
	}
	return request
}

// ConsumeAssistantStream waits for the provider's terminal settlement even after invocation cancellation. afterResponse cancellation waits for the shared cancellation operation and keeps the raw settlement; other failures propagate on this operation.
func ConsumeAssistantStream(ctx harness.Context, stream *ai.AssistantMessageEventStream, observer AssistantStreamObserver, afterResponse func(harness.Context, *ai.AssistantMessage) (*ai.AssistantMessage, error)) (*ai.AssistantMessage, error) {
	started := false
	for event := range stream.Events(context.WithoutCancel(ctx)) {
		switch value := event.(type) {
		case ai.StartEvent:
			if started {
				return nil, errors.New("Assistant message stream emitted more than one start event")
			}
			started = true
			// upstream: assistant.ts:112 passes { ...event.partial } at the observation boundary.
			if err := observer.Start(ctx, value.Partial.ShallowCopy(), value); err != nil {
				return nil, err
			}
		case ai.DoneEvent:
			if !started {
				return nil, errors.New("Assistant message stream emitted done before start")
			}
		case ai.ErrorEvent:
		default:
			if !started {
				return nil, fmt.Errorf("Assistant message stream emitted %s before start", event.EventType())
			}
			if err := observer.Update(ctx, assistantEventPartial(event).ShallowCopy(), event); err != nil {
				return nil, err
			}
		}
	}
	settled := stream.Result()
	finalMessage := settled
	if afterResponse != nil {
		message, err := afterResponse(ctx, settled)
		if err != nil {
			aborted, ok := err.(*AbortRequested) //nolint:errorlint // Upstream catches instanceof AbortRequested, not an unrelated error wrapping one.
			if !ok {
				return nil, err
			}
			if err := aborted.Cancellation(); err != nil {
				return nil, err
			}
		} else {
			finalMessage = message
		}
	}
	if err := observer.End(ctx, finalMessage); err != nil {
		return nil, err
	}
	return finalMessage, nil
}

func assistantEventPartial(event ai.AssistantMessageEvent) *ai.AssistantMessage {
	switch value := event.(type) {
	case ai.TextStartEvent:
		return value.Partial
	case ai.TextDeltaEvent:
		return value.Partial
	case ai.TextEndEvent:
		return value.Partial
	case ai.ThinkingStartEvent:
		return value.Partial
	case ai.ThinkingDeltaEvent:
		return value.Partial
	case ai.ThinkingEndEvent:
		return value.Partial
	case ai.ToolCallStartEvent:
		return value.Partial
	case ai.ToolCallDeltaEvent:
		return value.Partial
	case ai.ToolCallEndEvent:
		return value.Partial
	default:
		panic(fmt.Sprintf("unexpected assistant update event %T", event))
	}
}

// StreamHarnessAssistant streams one assistant response without mutating the caller's message list. Transformation, conversion, request, response transformation and observer callbacks are awaited in order.
func StreamHarnessAssistant(ctx harness.Context, messages []agent.AgentMessage, config HarnessAssistantStreamConfig) (*ai.AssistantMessage, error) {
	requestContext := AssistantRequestContext{Messages: slices.Clone(messages), SystemPrompt: config.SystemPrompt}
	if config.TransformContext != nil {
		var err error
		requestContext, err = config.TransformContext(ctx, requestContext)
		if err != nil {
			return nil, err
		}
	}
	providerMessages, err := config.ToProviderMessages(ctx, requestContext.Messages)
	if err != nil {
		return nil, err
	}
	aiContext := ai.Context{SystemPrompt: requestContext.SystemPrompt, Messages: providerMessages, Tools: config.Tools}
	metadata := AssistantResponseMetadata{}
	stream, err := config.Request(ctx, aiContext, createRequestOptions(ctx, config, func(next AssistantResponseMetadata) { metadata = next }))
	if err != nil {
		return nil, err
	}
	var afterResponse func(harness.Context, *ai.AssistantMessage) (*ai.AssistantMessage, error)
	if config.AfterResponse != nil {
		afterResponse = func(ctx harness.Context, message *ai.AssistantMessage) (*ai.AssistantMessage, error) {
			return config.AfterResponse(ctx, message, metadata)
		}
	}
	return ConsumeAssistantStream(ctx, stream, config.Observer, afterResponse)
}
