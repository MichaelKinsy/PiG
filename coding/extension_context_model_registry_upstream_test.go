package coding

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v1.1.0/packages/coding-agent/test/suite/regressions/8964-extension-provider-streaming.test.ts:7 (both rows): an extension command streams a response from a provider that pi.registerProvider registered, through ctx.modelRegistry.find and ctx.modelRegistry.stream or streamSimple. The provider receives the registered API key, and the command reads the deltas and the final message. The registry the command reaches is the Session's registry, typed as Pi's ModelRegistry.
func TestExtensionCommandStreamsThroughContextModelRegistryUpstream(t *testing.T) {
	for _, method := range []string{"stream", "streamSimple"} {
		t.Run(method, func(t *testing.T) {
			var registry extension.ModelRegistry
			var streamed strings.Builder
			var result *ai.AssistantMessage
			ext := extension.Extension{Commands: map[string]extension.RegisteredCommand{"stream-custom": {Name: "stream-custom", Description: "Stream a response from the custom provider", Handler: func(ctx context.Context, _ string) error {
				reg, err := extension.CommandContextFromContext(ctx).ModelRegistry()
				if err != nil {
					return err
				}
				registry = reg
				model := reg.Find("extension-provider", "faux")
				if model == nil {
					return errors.New("ctx.modelRegistry.find did not find the registered model")
				}
				request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}}
				var stream *ai.AssistantMessageEventStream
				if method == "streamSimple" {
					stream = reg.StreamSimple(ctx, model, request, ai.StreamOptions{})
				} else {
					stream = reg.Stream(ctx, model, request, ai.StreamOptions{})
				}
				for event := range stream.Events(ctx) {
					if delta, ok := event.(ai.TextDeltaEvent); ok {
						streamed.WriteString(delta.Delta)
					}
				}
				result = stream.Result()
				return nil
			}}}}
			h := newModelExtensionHarness(t, []bool{false}, "", true, ext, nil)
			var receivedKey string
			if err := h.runner.Runtime().RegisterProvider("extension-provider", extension.ProviderConfig{
				API: "issue-8964-extension-api", APIKey: "extension-key", BaseURL: "https://extension.invalid", Models: []extension.ProviderModelConfig{{ID: "faux", Name: "Faux", Input: []string{"text"}}},
				StreamSimple: func(_ extension.Model, _ extension.AIContext, raw extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
					receivedKey = raw.APIKey
					message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "custom provider response"}}, StopReason: ai.StopReasonStop}
					return newSessionTestStream(ai.StartEvent{Partial: message}, ai.TextStartEvent{ContentIndex: 0, Partial: message}, ai.TextDeltaEvent{ContentIndex: 0, Delta: "custom provider response", Partial: message}, ai.TextEndEvent{ContentIndex: 0, Content: "custom provider response", Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
				},
			}); err != nil {
				t.Fatal(err)
			}
			// Pi's harness constructs AgentSession, which binds the extension runner and flushes the queued registration.
			if err := h.session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
				t.Fatal(err)
			}
			if err := h.session.Prompt(t.Context(), "/stream-custom"); err != nil {
				t.Fatal(err)
			}
			if registry != extension.ModelRegistry(h.services.Registry()) {
				t.Fatalf("ctx.modelRegistry = %T %p, want the Session's registry %p", registry, registry, h.services.Registry())
			}
			if receivedKey != "extension-key" || streamed.String() != "custom provider response" {
				t.Fatalf("api key = %q, streamed = %q", receivedKey, streamed.String())
			}
			if result == nil || result.StopReason != ai.StopReasonStop || len(result.Content) != 1 || result.Content[0] != (ai.TextContent{Text: "custom provider response"}) {
				t.Fatalf("stream result = %+v", result)
			}
		})
	}
}

// ctx.modelRegistry has Pi's ModelRegistry members (model-registry.ts:48-243) and each one reads the Session's registry: the facade's synchronous reads, its awaited auth and refresh calls, and provider registration by name, which getRegisteredProviderConfig and getRegisteredProviderIds then report and unregisterProvider removes.
func TestExtensionContextModelRegistryHasPiMembers(t *testing.T) {
	var registry extension.ModelRegistry
	ext := extension.Extension{Commands: map[string]extension.RegisteredCommand{"registry": {Name: "registry", Handler: func(ctx context.Context, _ string) error {
		var err error
		registry, err = extension.CommandContextFromContext(ctx).ModelRegistry()
		return err
	}}}}
	h := newModelExtensionHarness(t, []bool{false, true}, "", true, ext, nil)
	if err := h.session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if err := h.session.Prompt(t.Context(), "/registry"); err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("ctx.modelRegistry is nil")
	}
	model := registry.Find("faux", "faux-2")
	if model == nil || model.ID != "faux-2" || registry.Find("faux", "missing") != nil {
		t.Fatalf("find = %+v", model)
	}
	result := registry.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	if result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	if registry.GetError() != "" || !registry.HasConfiguredAuth(model) || registry.IsUsingOAuth(model) {
		t.Fatalf("getError = %q, hasConfiguredAuth = %v, isUsingOAuth = %v", registry.GetError(), registry.HasConfiguredAuth(model), registry.IsUsingOAuth(model))
	}
	if registry.GetProviderDisplayName("faux") != "Faux" || registry.GetProviderDisplayName("missing") != "missing" {
		t.Fatalf("getProviderDisplayName = %q, %q", registry.GetProviderDisplayName("faux"), registry.GetProviderDisplayName("missing"))
	}
	if provider := registry.GetProvider("faux"); provider == nil || provider.ID != "faux" {
		t.Fatalf("getProvider = %+v", provider)
	}
	if key := registry.GetAPIKeyForProvider(t.Context(), "faux"); key == nil || *key != "faux-key" {
		t.Fatalf("getApiKeyForProvider = %v", key)
	}
	if auth := registry.GetAPIKeyAndHeaders(t.Context(), model); !auth.OK || auth.APIKey == nil || *auth.APIKey != "faux-key" {
		t.Fatalf("getApiKeyAndHeaders = %+v", auth)
	}
	ids := func(models []*ai.Model) []string {
		var out []string
		for _, m := range models {
			if m.ProviderID() == "faux" {
				out = append(out, m.ID)
			}
		}
		return out
	}
	if all, available := ids(registry.GetAll()), ids(registry.GetAvailable()); !slices.Equal(all, []string{"faux-1", "faux-2"}) || !slices.Equal(available, all) {
		t.Fatalf("getAll = %v, getAvailable = %v", all, available)
	}
	if got := registry.GetModelOfType(ai.ModelTypeChat, "faux", "faux-1"); got == nil {
		t.Fatal("getModelOfType(chat) = nil")
	}
	if err := registry.RegisterProvider("by-name", ProviderConfigInput{Name: "By Name", BaseURL: "https://by-name.invalid", APIKey: "k", API: ai.APIOpenAICompletions, Models: []ai.AnyModel{nativeCompatModel("m", "by-name", "https://by-name.invalid")}}); err != nil {
		t.Fatal(err)
	}
	if config := registry.GetRegisteredProviderConfig("by-name"); config == nil || config.Name != "By Name" || !slices.Contains(registry.GetRegisteredProviderIDs(), "by-name") || registry.GetProviderDisplayName("by-name") != "By Name" {
		t.Fatalf("getRegisteredProviderConfig = %+v, ids = %v", config, registry.GetRegisteredProviderIDs())
	}
	registry.UnregisterProvider("by-name")
	if registry.GetRegisteredProviderConfig("by-name") != nil || slices.Contains(registry.GetRegisteredProviderIDs(), "by-name") {
		t.Fatalf("after unregisterProvider: ids = %v", registry.GetRegisteredProviderIDs())
	}
}
