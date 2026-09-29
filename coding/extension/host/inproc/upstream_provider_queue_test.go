package inproc_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func runnerProviderModelConfig(t *testing.T) extension.ProviderConfig {
	t.Helper()
	// Original shared input: extensions-runner.test.ts:50-79.
	const input = `{"baseUrl":"https://provider.test/v1","apiKey":"provider-test-key","api":"openai-completions","models":[{"id":"instant-model","name":"Instant Model","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0.1,"cacheWrite":1.25,"tiers":[{"inputTokensAbove":272000,"input":2,"output":3,"cacheRead":0.2,"cacheWrite":2.5}]},"contextWindow":128000,"maxTokens":4096}]}`
	var config extension.ProviderConfig
	if err := json.Unmarshal([]byte(input), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestRunnerProviderQueueRetainsStreamCallback(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime := extension.CreateExtensionRuntime()
	config := runnerProviderModelConfig(t)
	calls := 0
	config.StreamSimple = func(_ extension.Model, _ extension.AIContext, options extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
		calls++
		if options.(ai.StreamOptions).APIKey != "provider-test-key" {
			t.Error("queued callback lost its request credentials")
		}
		stream := ai.NewAssistantMessageEventStream()
		stream.End(&ai.AssistantMessage{StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "queued producer"}}})
		return stream
	}
	if err := runtime.RegisterProvider("queued-stream", config); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(nil, services.CWD(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { t.Errorf("bind: %+v", err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
	if calls != 0 {
		t.Fatal("binding invoked a stream producer")
	}
	model := services.Registry().Find("queued-stream", "instant-model")
	result := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{}, ai.StreamOptions{}).Result()
	if result.StopReason != ai.StopReasonStop || ai.ContentText(result.Content) != "queued producer" || calls != 1 {
		t.Fatalf("queued producer calls=%d result=%+v", calls, result)
	}
}

func TestRunnerProviderActionsOverrideRegistry(t *testing.T) {
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime := extension.CreateExtensionRuntime()
	config := runnerProviderModelConfig(t)
	if err := runtime.RegisterProvider("queued", config, "owner.ts"); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("custom registration rejected")
	var registered, removed []string
	var reported []*extension.ExtensionError
	runner := inproc.NewRunner(nil, services.CWD(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, &extension.ProviderActions{
		RegisterProvider: func(name string, got extension.ProviderConfig) error {
			registered = append(registered, name)
			if !reflect.DeepEqual(got, config) {
				t.Error("provider action received a different configuration")
			}
			return sentinel
		},
		UnregisterProvider: func(name string) { removed = append(removed, name) },
	})
	if len(reported) != 1 || reported[0].ExtensionPath != "owner.ts" || reported[0].Error != sentinel.Error() {
		t.Fatalf("reported=%+v", reported)
	}
	if err := runtime.RegisterProvider("post", config); !errors.Is(err, sentinel) {
		t.Fatalf("post-bind error=%v", err)
	}
	runtime.UnregisterProvider("post")
	if !reflect.DeepEqual(registered, []string{"queued", "post"}) || !reflect.DeepEqual(removed, []string{"post"}) || services.Registry().Find("queued", "instant-model") != nil {
		t.Fatalf("provider actions register=%v unregister=%v", registered, removed)
	}
}

func TestUpstreamRunnerProviderRegistration(t *testing.T) {
	newServices := func(t *testing.T) *coding.Services {
		t.Helper()
		services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		return services
	}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1126
	t.Run("bindCore ignores invalid queued registrations and reports extension error", func(t *testing.T) {
		services := newServices(t)
		runtime := extension.CreateExtensionRuntime()
		if err := runtime.RegisterProvider("broken-provider", extension.ProviderConfig{StreamSimple: func(extension.Model, extension.AIContext, extension.SimpleStreamOptions) extension.AssistantMessageEventStream {
			t.Fatal("should not run")
			return nil
		}}, "/tmp/broken-extension.ts"); err != nil {
			t.Fatal(err)
		}
		runner := inproc.NewRunner(nil, services.CWD(), runtime)
		var errors []string
		runner.AddErrorListener(func(err *extension.ExtensionError) {
			if err.Event != "register_provider" {
				t.Errorf("event=%q", err.Event)
			}
			errors = append(errors, err.ExtensionPath+": "+err.Error)
		})
		runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
		want := []string{`/tmp/broken-extension.ts: Provider broken-provider: "api" is required when registering streamSimple.`}
		if !reflect.DeepEqual(errors, want) {
			t.Fatalf("errors=%q; want %q", errors, want)
		}
		if result := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted {
			t.Fatalf("refresh aborted: %+v", result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1149
	t.Run("pre-bind unregister removes all queued registrations for a provider", func(t *testing.T) {
		runtime := extension.CreateExtensionRuntime()
		first := runnerProviderModelConfig(t)
		second := first
		second.Models = []extension.ProviderModelConfig{{ID: "instant-model-2", Name: "Instant Model 2", Reasoning: false, Input: []string{"text"}, Cost: extension.ProviderModelCost{}, ContextWindow: 128000, MaxTokens: 4096}}
		for _, config := range []extension.ProviderConfig{first, second} {
			if err := runtime.RegisterProvider("queued-provider", config); err != nil {
				t.Fatal(err)
			}
		}
		pending := runtime.PendingProviderRegistrations()
		if len(pending) != 2 || pending[0].Config.Models[0].ID != "instant-model" || pending[1].Config.Models[0].ID != "instant-model-2" {
			t.Fatalf("queued registrations=%+v; want both original configurations", pending)
		}
		runtime.UnregisterProvider("queued-provider")
		if pending := runtime.PendingProviderRegistrations(); len(pending) != 0 {
			t.Fatalf("unregister retained entries: %+v", pending)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1173
	t.Run("post-bind register and unregister take effect immediately", func(t *testing.T) {
		services := newServices(t)
		runtime := extension.CreateExtensionRuntime()
		runner := inproc.NewRunner(nil, services.CWD(), runtime)
		runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: services.Registry()}, nil)
		if len(runtime.PendingProviderRegistrations()) != 0 {
			t.Fatal("bound runtime has pending registrations")
		}
		if err := runtime.RegisterProvider("instant-provider", runnerProviderModelConfig(t)); err != nil {
			t.Fatal(err)
		}
		if len(runtime.PendingProviderRegistrations()) != 0 {
			t.Fatal("post-bind registration was queued")
		}
		model := services.Registry().Find("instant-provider", "instant-model")
		want := []ai.CostTier{{InputTokensAbove: 272000, InputCostPer1M: 2, OutputCostPer1M: 3, CacheReadCostPer1M: 0.2, CacheWriteCostPer1M: 2.5}}
		if model == nil || !reflect.DeepEqual(model.Capabilities.CostTiers, want) {
			t.Fatalf("immediate model=%+v; want tiers=%+v", model, want)
		}
		runtime.UnregisterProvider("instant-provider")
		if model := services.Registry().Find("instant-provider", "instant-model"); model != nil {
			t.Fatalf("unregister retained model=%+v", model)
		}
	})
}
