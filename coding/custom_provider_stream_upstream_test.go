package coding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// Pi's composed provider runs an extension's streamSimple only for a model whose api is the api the extension registered it for; a model of the same provider with another api reaches that API's implementation (packages/coding-agent/src/core/provider-composer.ts:590-602). Both registration forms keep that split at every entry point that builds a registered provider's stream: the Model Runtime request, the typed provider from getProvider, and the provider a built model carries.
func TestRegisteredProviderCallbackServesOnlyItsAPI(t *testing.T) {
	var requests, callbacks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"from api\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	callback := func() *ai.AssistantMessageEventStream {
		callbacks.Add(1)
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "from callback"}}, StopReason: ai.StopReasonStop}
		return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
	}
	registrations := map[string]func(*Services) error{
		"legacy": func(services *Services) error {
			return services.Registry().RegisterProvider("mixed", extension.ProviderConfig{
				API: "mixed-api", BaseURL: server.URL, APIKey: "key",
				Models: []extension.ProviderModelConfig{{ID: "custom", Name: "Custom", ContextWindow: 128, MaxTokens: 16}, {ID: "stock", Name: "Stock", API: ai.APIOpenAICompletions, ContextWindow: 128, MaxTokens: 16}},
				StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
					return callback()
				},
			})
		},
		"native": func(services *Services) error {
			custom, stock := nativeCompatModel("custom", "mixed", server.URL), nativeCompatModel("stock", "mixed", server.URL)
			custom.ProviderMeta.API = "mixed-api"
			return services.ModelRuntime().RegisterProvider("mixed", ProviderConfigInput{
				API: "mixed-api", BaseURL: server.URL, APIKey: "key", Models: []ai.AnyModel{custom, stock},
				StreamSimple: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					return callback(), nil
				},
			})
		},
	}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}
	for registration, register := range registrations {
		services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		if err := register(services); err != nil {
			t.Fatal(err)
		}
		runtime := services.ModelRuntime()
		entryPoints := map[string]func(*ai.Model) *ai.AssistantMessage{
			"runtime streamSimple": func(model *ai.Model) *ai.AssistantMessage {
				return runtime.StreamSimple(t.Context(), model, request, ai.StreamOptions{}).Result()
			},
			"runtime stream": func(model *ai.Model) *ai.AssistantMessage {
				return runtime.Stream(t.Context(), model, request, ai.StreamOptions{}).Result()
			},
			"getProvider stream": func(model *ai.Model) *ai.AssistantMessage {
				stream, err := runtime.GetProvider("mixed").Stream(t.Context(), model, ai.NormalizeContext(request), ai.StreamOptions{APIKey: "key"})
				if err != nil {
					t.Fatal(err)
				}
				return stream.Result()
			},
			"built model": func(model *ai.Model) *ai.AssistantMessage {
				built, err := BuildModel("mixed/"+model.ID, services)
				if err != nil {
					t.Fatal(err)
				}
				stream, err := built.Provider.Stream(t.Context(), ai.NormalizeContext(request), ai.StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				return stream.Result()
			},
		}
		for name, run := range entryPoints {
			for _, tc := range []struct{ model, text string }{{"custom", "from callback"}, {"stock", "from api"}} {
				t.Run(registration+"/"+name+"/"+tc.model, func(t *testing.T) {
					callbacks.Store(0)
					requests.Store(0)
					model := runtime.GetModel("mixed", tc.model)
					if model == nil {
						t.Fatal("registered model missing")
					}
					result := run(model)
					wantCallbacks, wantRequests := int32(1), int32(0)
					if tc.model == "stock" {
						wantCallbacks, wantRequests = 0, 1
					}
					if callbacks.Load() != wantCallbacks || requests.Load() != wantRequests || result.StopReason != ai.StopReasonStop || lateProviderText(result) != tc.text {
						t.Fatalf("callbacks=%d requests=%d result=%+v", callbacks.Load(), requests.Load(), result)
					}
				})
			}
		}
	}
}
