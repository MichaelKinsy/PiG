package subprocess

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func providerOptionsWire(o ai.StreamOptions) map[string]any {
	values := map[string]any{"apiKey": o.APIKey}
	optional := map[string]any{"reasoning": o.Thinking, "reasoningEffort": o.ReasoningEffort, "effort": o.Effort, "thinkingDisplay": o.ThinkingDisplay, "cacheRetention": o.CacheRetention, "sessionId": o.SessionID, "transport": o.Transport}
	for key, value := range optional {
		if fmt.Sprint(value) != "" {
			values[key] = value
		}
	}
	if o.MaxTokens != 0 {
		values["maxTokens"] = o.MaxTokens
	}
	if o.TemperatureSet || o.Temperature != 0 {
		values["temperature"] = o.Temperature
	}
	if o.TimeoutMs != nil {
		values["timeoutMs"] = *o.TimeoutMs
	}
	if o.WebSocketConnectTimeoutMs != nil {
		values["websocketConnectTimeoutMs"] = *o.WebSocketConnectTimeoutMs
	}
	if o.MaxRetries != nil {
		values["maxRetries"] = *o.MaxRetries
	}
	if o.MaxRetryDelayMs != nil {
		values["maxRetryDelayMs"] = *o.MaxRetryDelayMs
	}
	if o.ThinkingEnabled != nil {
		values["thinkingEnabled"] = *o.ThinkingEnabled
	}
	if o.ThinkingBudgetTokens != nil {
		values["thinkingBudgetTokens"] = *o.ThinkingBudgetTokens
	}
	if o.InterleavedThinking != nil {
		values["interleavedThinking"] = *o.InterleavedThinking
	}
	if o.GoogleThinking != nil {
		values["thinking"] = o.GoogleThinking
	}
	if o.ThinkingBudgets != nil {
		values["thinkingBudgets"] = o.ThinkingBudgets
	}
	if o.Headers != nil {
		values["headers"] = o.Headers
	}
	if o.Env != nil {
		values["env"] = o.Env
	}
	if o.SamplingParams != nil {
		values["samplingParams"] = o.SamplingParams
	}
	if o.RequestMetadata != nil {
		values["requestMetadata"] = o.RequestMetadata
	}
	if o.Metadata != nil {
		values["metadata"] = o.Metadata
	}
	if o.ToolChoice != nil {
		values["toolChoice"] = o.ToolChoice
	}
	return values
}

// providerStreamCallback executes the provider in its owning extension. The connection owns and joins the worker; request cancellation removes correlation and cancels the remote callback.
// pig additive (D19): connection-owned callbacks carry legacy streamSimple execution across the SDK boundary without changing registry scope.
func (h *Host) providerStreamCallback(me *managedExt, name string) extension.ProviderStreamSimple {
	return func(model extension.Model, rawContext extension.AIContext, rawOptions extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		transcript := rawContext
		options := rawOptions
		parent := options.Signal
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithCancel(parent)
		stream := ai.NewAssistantMessageEventStream()
		fail := func(err error) {
			reason := ai.StopReasonError
			_ = stream.Push(ai.ErrorEvent{Reason: reason, Error: &ai.AssistantMessage{API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, Content: []ai.AssistantContentBlock{}, StopReason: reason, ErrorMessage: err.Error()}})
			cancel()
		}
		args, err := json.Marshal(map[string]any{"model": extension.ModelInfo(model), "context": map[string]any{"messages": transcript.Messages()}, "options": providerOptionsWire(options)})
		if err != nil {
			fail(err)
			return stream
		}
		conn := me.connection()
		if conn == nil {
			fail(fmt.Errorf("provider %q extension is disconnected", name))
			return stream
		}
		if !conn.startProducerTask(func() {
			defer cancel()
			response, err := conn.requestWithUpdates(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: RequestProviderStream, Tool: name, Args: args}}, func(raw json.RawMessage) {
				event, err := decodeNativeProviderEvent(raw)
				if err == nil {
					err = stream.Push(event)
				}
				if err != nil {
					fail(err)
				}
			})
			if err != nil {
				fail(err)
				return
			}
			if response == nil || response.Response == nil {
				fail(fmt.Errorf("provider %q returned no response", name))
				return
			}
			if response.Response.Error != nil {
				fail(response.Response.Error.ToError())
				return
			}
			var result ai.AssistantMessage
			if err := json.Unmarshal(response.Response.Result, &result); err != nil {
				fail(err)
				return
			}
			stream.End(&result)
		}) {
			fail(fmt.Errorf("provider %q extension is closing", name))
		}
		return stream
	}
}
