package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestRegisteredProviderCallbackPanicIsRequestError(t *testing.T) {
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	services.Registry().RegisterProvider("throws", extension.ProviderConfig{API: "custom-api", BaseURL: "https://extension.invalid", APIKey: "key", Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model"}}, StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		panic("callback failure")
	}})
	model := services.ModelRuntime().GetModel("throws", "model")
	result := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "callback failure") {
		t.Fatalf("result=%+v", result)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8964-extension-provider-streaming.test.ts:7 (both stream and streamSimple rows).
func TestRegisteredProviderCustomStreamUpstream(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple"} {
		t.Run(method, func(t *testing.T) {
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			var receivedKey string
			services.Registry().RegisterProvider("extension-provider", extension.ProviderConfig{
				API: "issue-8964-extension-api", APIKey: "extension-key", BaseURL: "https://extension.invalid", Models: []extension.ProviderModelConfig{{ID: "faux", Name: "Faux", Input: []string{"text"}}},
				StreamSimple: func(_ extension.Model, _ extension.AIContext, raw extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
					receivedKey = raw.(ai.StreamOptions).APIKey
					message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "custom provider response"}}, StopReason: ai.StopReasonStop}
					return newSessionTestStream(ai.StartEvent{Partial: message}, ai.TextStartEvent{ContentIndex: 0, Partial: message}, ai.TextDeltaEvent{ContentIndex: 0, Delta: "custom provider response", Partial: message}, ai.TextEndEvent{ContentIndex: 0, Content: "custom provider response", Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
				},
			})
			model := services.ModelRuntime().GetModel("extension-provider", "faux")
			if model == nil {
				t.Fatal("registered model missing")
			}
			request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}
			var stream *ai.AssistantMessageEventStream
			if method == "streamSimple" {
				stream = services.ModelRuntime().StreamSimple(t.Context(), model, request, ai.StreamOptions{})
			} else {
				stream = services.ModelRuntime().Stream(t.Context(), model, request, ai.StreamOptions{})
			}
			var text strings.Builder
			for event := range stream.Events(context.Background()) {
				if delta, ok := event.(ai.TextDeltaEvent); ok {
					text.WriteString(delta.Delta)
				}
			}
			result := stream.Result()
			if receivedKey != "extension-key" || text.String() != "custom provider response" || result.StopReason != ai.StopReasonStop {
				t.Fatalf("key=%q text=%q result=%+v", receivedKey, text.String(), result)
			}
		})
	}
}
