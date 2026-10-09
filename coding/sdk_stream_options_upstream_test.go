package coding

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// captureSDKStreamOptions mirrors captureStreamOptions of sdk-stream-options.test.ts:82. With a providerEvent the provider forwards it through onProviderStreamEvent and the request runs through Session.Prompt, as upstream does (:107-121, :130-139).
func captureSDKStreamOptions(t *testing.T, api ai.API, settings icodingagent.Settings, options ai.StreamOptions, runner *inproc.Runner, providerEvent ...any) ai.StreamOptions {
	t.Helper()
	services := newTestServicesWithSettings(t, settings)
	var captured ai.StreamOptions
	services.Registry().RegisterExtensionProvider("capture-provider", extension.ProviderConfig{API: api, BaseURL: "https://capture.invalid/v1", APIKey: "test-api-key", Headers: map[string]string{"x-provider": "provider"}, StreamSimple: func(model extension.Model, _ extension.AIContext, raw extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		captured = raw
		stream := ai.NewAssistantMessageEventStream()
		if len(providerEvent) != 0 {
			if captured.OnProviderStreamEvent != nil {
				if err := captured.OnProviderStreamEvent(context.Background(), providerEvent[0], model); err != nil {
					t.Errorf("onProviderStreamEvent: %v", err)
				}
			}
		}
		stream.End(&ai.AssistantMessage{API: api, Provider: "capture-provider", Model: "capture-model", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}}, StopReason: ai.StopReasonStop})
		return stream
	}})
	bound, err := buildModel("capture-provider/capture-model", services, "")
	if err != nil {
		t.Fatal(err)
	}
	model := &ai.Model{ID: "capture-model", DisplayName: "Capture Model", Provider: bound.Provider, ProviderMeta: ai.ProviderMetadata{ProviderID: "capture-provider", API: api, BaseURL: "https://capture.invalid/v1", Headers: map[string]string{"x-model": "model"}}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}}
	session, err := NewSession(services, SessionOptions{Model: model, Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if len(providerEvent) != 0 {
		if err := session.Prompt(t.Context(), "test"); err != nil {
			t.Fatal(err)
		}
		return captured
	}
	stream, err := cacheWarmingStreamFn(func() *Session { return session })(t.Context(), model, ai.TranscriptContext{}, options)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonStop {
		t.Fatalf("result=%+v", result)
	}
	return captured
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:203,209
func TestSDKStreamOptionsDefaultsTimeoutForEveryProvider(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICodexResponses, ai.APIOpenAICompletions} {
		t.Run(string(api), func(t *testing.T) {
			got := captureSDKStreamOptions(t, api, icodingagent.Settings{HTTPIdleTimeoutMs: new(1234)}, ai.StreamOptions{}, nil)
			if got.TimeoutMs == nil || *got.TimeoutMs != 1234 {
				t.Fatalf("timeoutMs=%v, want 1234", got.TimeoutMs)
			}
		})
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:215
func TestSDKRequestTimeoutOverridesIdleTimeout(t *testing.T) {
	got := captureSDKStreamOptions(t, ai.APIOpenAICodexResponses, icodingagent.Settings{HTTPIdleTimeoutMs: new(1234)}, ai.StreamOptions{TimeoutMs: new(0)}, nil)
	if got.TimeoutMs == nil || *got.TimeoutMs != 0 {
		t.Fatalf("timeoutMs=%v", got.TimeoutMs)
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:225
func TestSDKForwardsWebSocketConnectTimeoutFromSettings(t *testing.T) {
	got := captureSDKStreamOptions(t, ai.APIOpenAICodexResponses, icodingagent.Settings{WebSocketConnectTimeoutMs: new(1234)}, ai.StreamOptions{}, nil)
	if got.WebSocketConnectTimeoutMs == nil || *got.WebSocketConnectTimeoutMs != 1234 {
		t.Fatalf("websocketConnectTimeoutMs=%v", got.WebSocketConnectTimeoutMs)
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:231
func TestSDKRequestWebSocketTimeoutOverridesSettings(t *testing.T) {
	got := captureSDKStreamOptions(t, ai.APIOpenAICodexResponses, icodingagent.Settings{WebSocketConnectTimeoutMs: new(1234)}, ai.StreamOptions{WebSocketConnectTimeoutMs: new(0)}, nil)
	if got.WebSocketConnectTimeoutMs == nil || *got.WebSocketConnectTimeoutMs != 0 {
		t.Fatalf("websocketConnectTimeoutMs=%v", got.WebSocketConnectTimeoutMs)
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:241
func TestSDKForwardsProviderRetrySettings(t *testing.T) {
	got := captureSDKStreamOptions(t, ai.APIOpenAICompletions, icodingagent.Settings{Retry: &icodingagent.RetrySettingsJSON{Provider: &icodingagent.ProviderRetrySettings{MaxRetries: new(2), MaxRetryDelayMs: new(3000)}}}, ai.StreamOptions{}, nil)
	if got.MaxRetries == nil || *got.MaxRetries != 2 || got.MaxRetryDelayMs == nil || *got.MaxRetryDelayMs != 3000 {
		t.Fatalf("retry=(%v,%v)", got.MaxRetries, got.MaxRetryDelayMs)
	}
}

// sdk.ts:329 `options.maxRetries ?? providerRetrySettings.maxRetries`: an unset setting stays undefined, so the provider's own default applies, while an explicit 0 reaches the request as 0.
func TestSDKLeavesAnUnsetProviderMaxRetriesUnset(t *testing.T) {
	if got := captureSDKStreamOptions(t, ai.APIOpenAICompletions, icodingagent.Settings{}, ai.StreamOptions{}, nil); got.MaxRetries != nil {
		t.Fatalf("unset maxRetries = %d, want nil", *got.MaxRetries)
	}
	got := captureSDKStreamOptions(t, ai.APIOpenAICompletions, icodingagent.Settings{Retry: &icodingagent.RetrySettingsJSON{Provider: &icodingagent.ProviderRetrySettings{MaxRetries: new(0)}}}, ai.StreamOptions{}, nil)
	if got.MaxRetries == nil || *got.MaxRetries != 0 {
		t.Fatalf("explicit maxRetries = %v, want 0", got.MaxRetries)
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:250
// Pi: packages/coding-agent/src/core/extensions/types.ts:892 (BeforeProviderHeadersEvent.headers).
func TestSDKStreamOptionsRunsHeadersHookOnAssembledHeaders(t *testing.T) {
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/headers", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
		headers := args[0].(extension.BeforeProviderHeadersEvent).Headers
		value := ""
		for _, key := range []string{"x-provider", "x-model", "x-explicit"} {
			if value != "" {
				value += ":"
			}
			if part := headers[key]; part != nil {
				value += *part
			}
		}
		headers["x-hook"] = new(value)
		return nil, nil
	}}}}}, t.TempDir())
	got := captureSDKStreamOptions(t, ai.APIOpenAICompletions, icodingagent.Settings{}, ai.StreamOptions{Headers: ai.ProviderHeaders{"x-explicit": new("explicit")}}, runner)
	for key, want := range map[string]string{"x-provider": "provider", "x-model": "model", "x-explicit": "explicit", "x-hook": "provider:model:explicit"} {
		if value := got.Headers[key]; value == nil || *value != want {
			t.Errorf("%s=%v want %q", key, value, want)
		}
	}
	if got.TransformHeaders != nil {
		t.Fatal("transformHeaders forwarded")
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/sdk-stream-options.test.ts:271-298 (regression test for #9784).
// Per-case difference: upstream loads the extension from a factory through DefaultResourceLoader; Go binds an extension with the same handler through inproc.NewRunner, the way the header case above already does.
func TestSDKForwardsProviderStreamEventsToExtensions(t *testing.T) {
	providerEvent := map[string]any{"openrouter_metadata": map[string]any{"strategy": "direct"}}
	var mu sync.Mutex
	var extensionEvents []extension.ProviderStreamEvent
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/provider-stream-event", Handlers: map[string][]extension.HandlerFn{"provider_stream_event": {func(args ...any) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		extensionEvents = append(extensionEvents, args[0].(extension.ProviderStreamEvent))
		return nil, nil
	}}}}}, t.TempDir())
	options := captureSDKStreamOptions(t, ai.APIOpenAICompletions, icodingagent.Settings{}, ai.StreamOptions{}, runner, providerEvent)
	if options.OnProviderStreamEvent == nil {
		t.Fatal("the provider was not given onProviderStreamEvent")
	}
	mu.Lock()
	defer mu.Unlock()
	want := []extension.ProviderStreamEvent{{Type: "provider_stream_event", Provider: "capture-provider", API: "openai-completions", Model: "capture-model", Data: providerEvent}}
	if !reflect.DeepEqual(extensionEvents, want) {
		t.Fatalf("extension events = %#v, want %#v", extensionEvents, want)
	}
}
