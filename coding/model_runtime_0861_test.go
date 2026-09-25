package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type runtimeTestProvider struct {
	id       string
	stream   func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error)
	calls    atomic.Int32
	mu       sync.Mutex
	messages []ai.Message
	options  ai.StreamOptions
}

func (provider *runtimeTestProvider) ID() string   { return provider.id }
func (provider *runtimeTestProvider) Close() error { return nil }
func (provider *runtimeTestProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	provider.calls.Add(1)
	provider.mu.Lock()
	provider.messages = transcript.Messages()
	provider.options = options
	provider.mu.Unlock()
	return provider.stream(ctx, transcript, options)
}

func runtimeTestMessage(provider, model, text string, reason ai.StopReason) *ai.AssistantMessage {
	content := []ai.AssistantContentBlock(nil)
	if text != "" {
		content = append(content, ai.TextContent{Text: text})
	}
	return &ai.AssistantMessage{Content: content, API: ai.APIOpenAICompletions, Provider: provider, Model: model, StopReason: reason}
}

func runtimeTestTextStream(provider, model, text string) (*ai.AssistantMessageEventStream, *ai.AssistantMessage) {
	start := runtimeTestMessage(provider, model, "", ai.StopReasonPending)
	partial := runtimeTestMessage(provider, model, text, ai.StopReasonPending)
	final := runtimeTestMessage(provider, model, text, ai.StopReasonStop)
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: start},
		ai.TextStartEvent{ContentIndex: 0, Partial: partial},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: text, Partial: partial},
		ai.TextEndEvent{ContentIndex: 0, Content: text, Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: final},
	} {
		if err := stream.Push(event); err != nil {
			panic(err)
		}
	}
	return stream, final
}

func newRuntimeTestServices(t *testing.T) *Services {
	t.Helper()
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return services
}

func collectRuntimeEvents(ctx context.Context, stream *ai.AssistantMessageEventStream) []ai.AssistantMessageEvent {
	var events []ai.AssistantMessageEvent
	for event := range stream.Events(ctx) {
		events = append(events, event)
	}
	return events
}

func TestModelRuntimeStreamReturnsBeforeSetupAndForwardsTypedEvents(t *testing.T) {
	services := newRuntimeTestServices(t)
	runtime := services.ModelRuntime()
	setupStarted := make(chan struct{})
	releaseSetup := make(chan struct{})
	provider := &runtimeTestProvider{id: "test", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream, _ := runtimeTestTextStream("test", "model", "ok")
		return stream, nil
	}}
	model := &ai.Model{ID: "model", Provider: provider}
	runtime.prepare = func(ctx context.Context, model *ai.Model, options ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		close(setupStarted)
		select {
		case <-ctx.Done():
			return nil, nil, ai.StreamOptions{}, ctx.Err()
		case <-releaseSetup:
			return model, provider, options, nil
		}
	}

	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("original")}}}
	returned := make(chan *ai.AssistantMessageEventStream, 1)
	go func() { returned <- runtime.Stream(context.Background(), model, request, ai.StreamOptions{}) }()
	var stream *ai.AssistantMessageEventStream
	select {
	case stream = <-returned:
	case <-time.After(time.Second):
		t.Fatal("Stream blocked on request setup")
	}
	<-setupStarted
	request.Messages[0] = ai.UserMessage{Content: ai.UserText("mutated")}
	close(releaseSetup)

	events := collectRuntimeEvents(context.Background(), stream)
	want := []ai.AssistantEventType{ai.EventStart, ai.EventTextStart, ai.EventTextDelta, ai.EventTextEnd, ai.EventDone}
	got := make([]ai.AssistantEventType, len(events))
	for i, event := range events {
		got[i] = event.EventType()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if provider.messages[0].(ai.UserMessage).Content.(ai.UserText) != "original" {
		t.Fatalf("provider transcript = %#v", provider.messages)
	}
}

func TestModelRuntimeUnknownProviderIsLazyTerminalError(t *testing.T) {
	runtime := newRuntimeTestServices(t).ModelRuntime()
	model := &ai.Model{ID: "missing", ProviderMeta: ai.ProviderMetadata{ProviderID: "missing", API: ai.APIOpenAIResponses}}
	stream := runtime.Stream(context.Background(), model, ai.Context{}, ai.StreamOptions{})
	events := collectRuntimeEvents(context.Background(), stream)
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	errorEvent, ok := events[0].(ai.ErrorEvent)
	if !ok || !strings.Contains(errorEvent.Error.ErrorMessage, "missing") || stream.Result() != errorEvent.Error {
		t.Fatalf("terminal = %#v result=%p", events[0], stream.Result())
	}
}

func TestModelRuntimeMissingAuthIsLazyTerminalError(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	services := newRuntimeTestServices(t)
	provider := &runtimeTestProvider{id: "openai", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		t.Fatal("provider called without auth")
		return nil, nil
	}}
	model := &ai.Model{ID: "gpt", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: "openai", API: ai.APIOpenAIResponses}}
	stream := services.ModelRuntime().Stream(context.Background(), model, ai.Context{}, ai.StreamOptions{})
	result := stream.Result()
	if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, "openai") || provider.calls.Load() != 0 {
		t.Fatalf("result = %#v calls=%d", result, provider.calls.Load())
	}
}

func TestModelRuntimeProviderSetupErrorBecomesOneTerminalError(t *testing.T) {
	services := newRuntimeTestServices(t)
	provider := &runtimeTestProvider{id: "test", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return nil, errors.New("setup failed")
	}}
	model := &ai.Model{ID: "model", Provider: provider}
	stream := services.ModelRuntime().Stream(context.Background(), model, ai.Context{}, ai.StreamOptions{})
	events := collectRuntimeEvents(context.Background(), stream)
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	errorEvent := events[0].(ai.ErrorEvent)
	if errorEvent.Error != stream.Result() || errorEvent.Error.ErrorMessage != "setup failed" {
		t.Fatalf("event = %#v result=%#v", errorEvent, stream.Result())
	}
}

func TestModelRuntimeCompleteReturnsExactStreamResultPointer(t *testing.T) {
	services := newRuntimeTestServices(t)
	provider := &runtimeTestProvider{id: "test"}
	var final *ai.AssistantMessage
	provider.stream = func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream, terminal := runtimeTestTextStream("test", "model", "complete")
		final = terminal
		return stream, nil
	}
	model := &ai.Model{ID: "model", Provider: provider}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{}, ai.StreamOptions{})
	if result != final {
		t.Fatalf("result=%p final=%p", result, final)
	}
}

func TestModelRuntimeStreamSimpleUsesSamePreparationAndResultIdentity(t *testing.T) {
	services := newRuntimeTestServices(t)
	runtime := services.ModelRuntime()
	provider := &runtimeTestProvider{id: "test"}
	var prepared atomic.Int32
	var final *ai.AssistantMessage
	provider.stream = func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream, terminal := runtimeTestTextStream("test", "model", "simple")
		final = terminal
		return stream, nil
	}
	model := &ai.Model{ID: "model", Provider: provider}
	runtime.prepare = func(context.Context, *ai.Model, ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		prepared.Add(1)
		return model, provider, ai.StreamOptions{}, nil
	}
	result := runtime.CompleteSimple(context.Background(), model, ai.Context{}, ai.StreamOptions{})
	if prepared.Load() != 1 || provider.calls.Load() != 1 || result != final {
		t.Fatalf("prepared=%d calls=%d result=%p final=%p", prepared.Load(), provider.calls.Load(), result, final)
	}
}

func TestModelRuntimeMergesHeadersAndEnvironmentAndConsumesTransform(t *testing.T) {
	services := newRuntimeTestServices(t)
	provider := &runtimeTestProvider{id: "test", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream, _ := runtimeTestTextStream("test", "model", "ok")
		return stream, nil
	}}
	model := &ai.Model{ID: "model", Provider: provider, ProviderMeta: ai.ProviderMetadata{Headers: map[string]string{"X-Token": "configured", "X-Keep": "yes", "X-Delete": "configured", "X-Transform-Delete": "configured"}}}
	var transforms atomic.Int32
	options := ai.StreamOptions{
		Headers: ai.ProviderHeaders{"x-token": new("request"), "x-delete": nil},
		Env:     ai.ProviderEnv{"REQUEST": "yes"},
		TransformHeaders: func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
			transforms.Add(1)
			headers["X-Transformed"] = new("yes")
			headers["X-Transform-Delete"] = nil
			return headers, nil
		},
	}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{}, options)
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	if transforms.Load() != 1 || provider.options.TransformHeaders != nil {
		t.Fatalf("transforms=%d forwarded=%v", transforms.Load(), provider.options.TransformHeaders != nil)
	}
	wantHeaders := ai.ProviderHeaders{"x-token": new("request"), "X-Keep": new("yes"), "x-delete": nil, "X-Transform-Delete": nil, "X-Transformed": new("yes")}
	if !reflect.DeepEqual(provider.options.Headers, wantHeaders) || provider.options.Env["REQUEST"] != "yes" {
		t.Fatalf("options = %#v", provider.options)
	}
}

func TestModelRuntimePreparesRealProviderWireRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		api  string
		sse  string
	}{
		{"openai completions", "openai-completions", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"openai responses", "openai-responses", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotHeader http.Header
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				gotHeader = request.Header.Clone()
				if err := json.NewDecoder(request.Body).Decode(&gotBody); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tc.sse)
			}))
			defer server.Close()

			agentDir := t.TempDir()
			models := fmt.Sprintf(`{"providers":{"wire":{"baseUrl":%q,"api":%q,"authHeader":false,"headers":{"X-Configured":"configured"},"compat":{"sendSessionAffinityHeaders":true,"supportsLongCacheRetention":true},"models":[{"id":"model","name":"Model"}]}}}`, server.URL+"/v1", tc.api)
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			model, err := BuildModel("wire/model", services)
			if err != nil {
				t.Fatal(err)
			}
			var transforms atomic.Int32
			result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{
				SessionID: "request-session",
				Env:       ai.ProviderEnv{"PI_CACHE_RETENTION": "long"},
				Headers:   ai.ProviderHeadersFromStrings(map[string]string{"x-configured": "request", "X-Request": "yes"}),
				TransformHeaders: func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
					transforms.Add(1)
					if headers["x-configured"] == nil || *headers["x-configured"] != "request" {
						t.Fatalf("pre-transform headers = %#v", headers)
					}
					headers["X-Transformed"] = new("yes")
					return headers, nil
				},
			})
			if result.StopReason != ai.StopReasonStop {
				t.Fatalf("result = %#v", result)
			}
			if transforms.Load() != 1 {
				t.Fatalf("header transforms = %d", transforms.Load())
			}
			if gotHeader.Get("X-Configured") != "request" || gotHeader.Get("X-Request") != "yes" || gotHeader.Get("X-Transformed") != "yes" {
				t.Fatalf("wire headers = %#v", gotHeader)
			}
			if gotHeader.Get("Authorization") != "" {
				t.Fatalf("authHeader:false sent authorization: %q", gotHeader.Get("Authorization"))
			}
			if gotBody["prompt_cache_retention"] != "24h" {
				t.Fatalf("wire body = %#v", gotBody)
			}
		})
	}
}

func TestModelRuntimeRequestModelRefreshDoesNotMutateOriginal(t *testing.T) {
	agentDir := t.TempDir()
	writeModels := func(baseURL string) {
		data := `{"providers":{"custom":{"baseUrl":"` + baseURL + `","apiKey":"key","api":"openai-completions","models":[{"id":"model","name":"Model"}]}}}`
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeModels("https://first.example/v1")
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model, err := BuildModel("custom/model", services)
	if err != nil {
		t.Fatal(err)
	}
	writeModels("https://second.example/v1")
	services.Registry().Refresh()
	prepared, _, _, err := services.ModelRuntime().prepareRequest(context.Background(), model, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model.ProviderMeta.BaseURL != "https://first.example/v1" || prepared.ProviderMeta.BaseURL != "https://second.example/v1" || prepared == model {
		t.Fatalf("original=%q prepared=%q same=%v", model.ProviderMeta.BaseURL, prepared.ProviderMeta.BaseURL, prepared == model)
	}
}

func TestModelRuntimeCancellationDuringSetupDoesNotInvokeProvider(t *testing.T) {
	services := newRuntimeTestServices(t)
	runtime := services.ModelRuntime()
	provider := &runtimeTestProvider{id: "test", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		t.Fatal("provider called after setup cancellation")
		return nil, nil
	}}
	started := make(chan struct{})
	runtime.prepare = func(ctx context.Context, model *ai.Model, options ai.StreamOptions) (*ai.Model, ai.Provider, ai.StreamOptions, error) {
		close(started)
		<-ctx.Done()
		return nil, nil, ai.StreamOptions{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := runtime.Stream(ctx, &ai.Model{ID: "model", Provider: provider}, ai.Context{}, ai.StreamOptions{})
	<-started
	cancel()
	result := stream.Result()
	if result.StopReason != ai.StopReasonError || provider.calls.Load() != 0 {
		t.Fatalf("result=%#v calls=%d", result, provider.calls.Load())
	}
}

func TestModelRuntimeCancellationAfterInnerCreationPreservesAssignedOrder(t *testing.T) {
	services := newRuntimeTestServices(t)
	providerStarted := make(chan struct{})
	providerCancelled := make(chan struct{})
	provider := &runtimeTestProvider{id: "test", stream: func(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream := ai.NewAssistantMessageEventStream()
		partial := runtimeTestMessage("test", "model", "", ai.StopReasonPending)
		if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
			return nil, err
		}
		close(providerStarted)
		go func() {
			<-ctx.Done()
			close(providerCancelled)
			message := runtimeTestMessage("test", "model", "", ai.StopReasonError)
			message.ErrorMessage = ctx.Err().Error()
			_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonError, Error: message})
		}()
		return stream, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	stream := services.ModelRuntime().Stream(ctx, &ai.Model{ID: "model", Provider: provider}, ai.Context{}, ai.StreamOptions{})
	<-providerStarted
	cancel()
	<-providerCancelled
	result := stream.Result()
	events := collectRuntimeEvents(context.Background(), stream)
	if len(events) != 2 || events[0].EventType() != ai.EventStart || events[1].EventType() != ai.EventError {
		t.Fatalf("events = %#v", events)
	}
	if result.StopReason != ai.StopReasonError {
		t.Fatalf("result = %#v", result)
	}
}

func TestSessionModelRuntimeLooksUpCurrentRegisteredAndUnknownModels(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"registry-test":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","authHeader":false,"models":[{"id":"current","name":"Current"},{"id":"non-current","name":"Non-current"},{"id":"org/model/name","name":"Slash model"}],"modelOverrides":{"override-only":{"name":"Must stay absent"}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	current, err := BuildModel("registry-test/current", services)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{Model: current, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	for _, modelID := range []string{"current", "non-current", "org/model/name"} {
		model := session.ModelRuntime().GetModel("registry-test", modelID)
		if model == nil || model.ID != modelID || model.ProviderMeta.ProviderID != "registry-test" {
			t.Fatalf("GetModel(registry-test, %s) = %#v", modelID, model)
		}
	}
	for _, modelID := range []string{"unknown", "override-only"} {
		if model := session.ModelRuntime().GetModel("registry-test", modelID); model != nil {
			t.Fatalf("GetModel(registry-test, %s) = %#v, want nil", modelID, model)
		}
	}
	catalog := session.ModelRuntime().GetModels()
	var registered []string
	for _, model := range catalog {
		if model.ProviderMeta.ProviderID == "registry-test" {
			registered = append(registered, model.ID)
		}
	}
	if !reflect.DeepEqual(registered, []string{"current", "non-current", "org/model/name"}) {
		t.Fatalf("registry-test catalog = %v", registered)
	}
}

func TestSessionModelRuntimeAppliesOnlyKnownGeneratedModelOverrides(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openrouter":{"modelOverrides":{"anthropic/claude-sonnet-4":{"name":"Configured Sonnet"},"nonexistent/model-id":{"name":"Must stay absent"}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("openrouter", "anthropic/claude-sonnet-4")
	if model == nil || model.DisplayName != "Configured Sonnet" {
		t.Fatalf("known generated override = %#v", model)
	}
	if model := services.ModelRuntime().GetModel("openrouter", "nonexistent/model-id"); model != nil {
		t.Fatalf("unknown generated override = %#v, want nil", model)
	}
	var knownCount int
	for _, model := range services.ModelRuntime().GetModels() {
		if model.ProviderMeta.ProviderID != "openrouter" {
			continue
		}
		switch model.ID {
		case "anthropic/claude-sonnet-4":
			knownCount++
			if model.DisplayName != "Configured Sonnet" {
				t.Fatalf("catalog override name = %q", model.DisplayName)
			}
		case "nonexistent/model-id":
			t.Fatalf("catalog contains unknown override: %#v", model)
		}
	}
	if knownCount != 1 {
		t.Fatalf("known generated model count = %d, want 1", knownCount)
	}
}

func TestModelRegistryDelegatesToBoundRuntime(t *testing.T) {
	services := newRuntimeTestServices(t)
	provider := &runtimeTestProvider{id: "test", stream: func(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		stream, _ := runtimeTestTextStream("test", "model", "registry")
		return stream, nil
	}}
	model := &ai.Model{ID: "model", Provider: provider}
	registry := services.Registry()
	if registry.runtime != services.ModelRuntime() {
		t.Fatal("registry is not bound to services runtime")
	}
	if result := registry.Complete(context.Background(), model, ai.Context{}, ai.StreamOptions{}); result.Content[0].(ai.TextContent).Text != "registry" {
		t.Fatalf("complete result = %#v", result)
	}
	if result := registry.Stream(context.Background(), model, ai.Context{}, ai.StreamOptions{}).Result(); result.Content[0].(ai.TextContent).Text != "registry" {
		t.Fatalf("stream result = %#v", result)
	}
	if result := registry.StreamSimple(context.Background(), model, ai.Context{}, ai.StreamOptions{}).Result(); result.Content[0].(ai.TextContent).Text != "registry" {
		t.Fatalf("streamSimple result = %#v", result)
	}
	if provider.calls.Load() != 3 {
		t.Fatalf("provider calls = %d, want 3", provider.calls.Load())
	}
}

func TestGeneratedOverridePreservesPresenceAndPartialFields(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"cloudflare-ai-gateway":{"modelOverrides":{"gpt-5.6-luna":{"name":"Configured Luna","reasoning":false,"thinkingLevelMap":{"high":"configured-high"},"input":[],"cost":{"input":0},"contextWindow":321000,"maxTokens":1234,"samplingParams":{"temperature":0},"compat":{"supportsStrictMode":false}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("cloudflare-ai-gateway", "gpt-5.6-luna")
	if model == nil {
		t.Fatal("known generated model is absent")
	}
	if model.DisplayName != "Configured Luna" {
		t.Errorf("name = %q, want Configured Luna", model.DisplayName)
	}
	if model.ProviderMeta.Reasoning {
		t.Error("explicit reasoning=false was replaced by the generated true value")
	}
	if model.Capabilities.InputCostPer1M != 0 {
		t.Errorf("input cost = %v, want explicit zero", model.Capabilities.InputCostPer1M)
	}
	if model.Capabilities.OutputCostPer1M != 1.2 || model.Capabilities.CacheReadCostPer1M != 0.02 || model.Capabilities.CacheWriteCostPer1M != 0.25 {
		t.Errorf("unmentioned generated costs were not preserved: output=%v cacheRead=%v cacheWrite=%v", model.Capabilities.OutputCostPer1M, model.Capabilities.CacheReadCostPer1M, model.Capabilities.CacheWriteCostPer1M)
	}
	if len(model.Capabilities.CostTiers) != 1 || model.Capabilities.CostTiers[0].InputTokensAbove != 272000 {
		t.Errorf("generated cost tiers = %#v, want preserved tier", model.Capabilities.CostTiers)
	}
	if model.Capabilities.ContextWindow != 321000 || model.Capabilities.MaxOutputTokens != 1234 {
		t.Errorf("limits = %d/%d, want 321000/1234", model.Capabilities.ContextWindow, model.Capabilities.MaxOutputTokens)
	}
	if model.Capabilities.SupportsImages {
		t.Error("explicit empty input list retained generated image support")
	}
	if got := model.ThinkingLevelMap[ai.ThinkingHigh]; got == nil || *got != "configured-high" {
		t.Errorf("high thinking mapping = %v, want configured-high", got)
	}
	if got := model.ThinkingLevelMap[ai.ThinkingLow]; got == nil || *got != "low" {
		t.Errorf("unmentioned low thinking mapping = %v, want low", got)
	}
	if value, ok := model.SamplingParams["temperature"]; !ok || value != float64(0) {
		t.Errorf("sampling temperature = %#v, want explicit zero", value)
	}
	if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsStrictMode == nil || *model.ProviderMeta.Compat.SupportsStrictMode {
		t.Errorf("supportsStrictMode = %#v, want explicit false", model.ProviderMeta.Compat)
	}
	var catalogModel *ai.Model
	for _, candidate := range services.ModelRuntime().GetModels() {
		if candidate.ProviderMeta.ProviderID == "cloudflare-ai-gateway" && candidate.ID == "gpt-5.6-luna" {
			catalogModel = candidate
			break
		}
	}
	if catalogModel == nil {
		t.Fatal("configured generated model is absent from catalog")
	}
	if catalogModel.DisplayName != model.DisplayName || catalogModel.ProviderMeta.Reasoning != model.ProviderMeta.Reasoning || catalogModel.Capabilities.InputCostPer1M != model.Capabilities.InputCostPer1M {
		t.Fatalf("catalog model = %#v, want direct lookup metadata", catalogModel)
	}
}

func TestGeneratedOverrideCanClearCostTiers(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"cloudflare-ai-gateway":{"modelOverrides":{"gpt-5.6-luna":{"cost":{"tiers":[]}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("cloudflare-ai-gateway", "gpt-5.6-luna")
	if model == nil {
		t.Fatal("known generated model is absent")
	}
	if len(model.Capabilities.CostTiers) != 0 {
		t.Fatalf("cost tiers = %#v, want explicit empty list", model.Capabilities.CostTiers)
	}
}

func TestGeneratedOverrideMergesPromptCache(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"anthropic":{"modelOverrides":{"claude-fable-5":{"promptCache":{"short":120},"compat":{"supportsStrictTools":false}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("anthropic", "claude-fable-5")
	if model == nil {
		t.Fatal("known generated model is absent")
	}
	if model.PromptCache["short"] != 120 || model.PromptCache["long"] != 3600 {
		t.Fatalf("prompt cache = %#v, want short=120 and preserved long=3600", model.PromptCache)
	}
	if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsStrictTools == nil || *model.ProviderMeta.Compat.SupportsStrictTools {
		t.Fatalf("supportsStrictTools = %#v, want explicit false", model.ProviderMeta.Compat)
	}
}

func TestExplicitModelOverrideMergesPartialCost(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://models.invalid/v1","api":"openai-completions","models":[{"id":"model","cost":{"input":2,"output":4,"cacheRead":1,"cacheWrite":3}}],"modelOverrides":{"model":{"cost":{"input":0}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("custom", "model")
	if model == nil {
		t.Fatal("explicit model is absent")
	}
	if model.Capabilities.InputCostPer1M != 0 || model.Capabilities.OutputCostPer1M != 4 || model.Capabilities.CacheReadCostPer1M != 1 || model.Capabilities.CacheWriteCostPer1M != 3 {
		t.Fatalf("cost = %#v, want input=0 output=4 cacheRead=1 cacheWrite=3", model.Capabilities)
	}
}

func TestModelRegistryChangeListenerCoversRegistrationAndRemoval(t *testing.T) {
	services := newRuntimeTestServices(t)
	registry := services.Registry()
	var notifications atomic.Int32
	detach := registry.SetChangeListener(func() {
		_ = services.ModelRuntime().GetModels()
		notifications.Add(1)
	})
	defer detach()
	registry.RegisterProvider("dynamic", extension.ProviderConfig{
		BaseURL: "https://models.invalid/v1",
		Models:  []extension.ProviderModelConfig{{ID: "model", Name: "Model"}},
	})
	if got := notifications.Load(); got != 1 {
		t.Fatalf("notifications after registration = %d, want 1", got)
	}
	registry.UnregisterProvider("dynamic")
	if got := notifications.Load(); got != 2 {
		t.Fatalf("notifications after removal = %d, want 2", got)
	}
}

func TestModelRegistryChangeListenerDetachCannotClearReplacement(t *testing.T) {
	services := newRuntimeTestServices(t)
	registry := services.Registry()
	var oldNotifications atomic.Int32
	var currentNotifications atomic.Int32
	detachOld := registry.SetChangeListener(func() { oldNotifications.Add(1) })
	detachCurrent := registry.SetChangeListener(func() { currentNotifications.Add(1) })
	detachOld()
	registry.RegisterProvider("dynamic", extension.ProviderConfig{BaseURL: "https://models.invalid/v1"})
	if got := oldNotifications.Load(); got != 0 {
		t.Fatalf("replaced listener notifications = %d, want 0", got)
	}
	if got := currentNotifications.Load(); got != 1 {
		t.Fatalf("current listener notifications = %d, want 1", got)
	}
	detachCurrent()
	registry.UnregisterProvider("dynamic")
	if got := currentNotifications.Load(); got != 1 {
		t.Fatalf("detached listener notifications = %d, want 1", got)
	}
}

func TestModelRegistryChangeListenerDetachDrainsActivePublication(t *testing.T) {
	services := newRuntimeTestServices(t)
	registry := services.Registry()
	entered := make(chan struct{})
	release := make(chan struct{})
	detach := registry.SetChangeListener(func() {
		close(entered)
		<-release
	})
	changed := make(chan struct{})
	go func() {
		registry.RegisterProvider("dynamic", extension.ProviderConfig{BaseURL: "https://models.invalid/v1"})
		close(changed)
	}()
	<-entered
	detached := make(chan struct{})
	go func() {
		detach()
		close(detached)
	}()
	select {
	case <-detached:
		t.Fatal("detach returned while publication was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("registry change did not complete")
	}
	select {
	case <-detached:
	case <-time.After(time.Second):
		t.Fatal("detach did not drain publication")
	}
}

func TestGeneratedProviderOverlayAppearsInFullCatalog(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"cloudflare-ai-gateway":{"baseUrl":"https://proxy.invalid/v1","headers":{"X-Configured":"yes"},"compat":{"supportsStrictMode":false}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	direct := services.ModelRuntime().GetModel("cloudflare-ai-gateway", "gpt-5.6-luna")
	if direct == nil {
		t.Fatal("direct lookup returned nil")
	}
	if direct.ProviderMeta.BaseURL != "https://proxy.invalid/v1" {
		t.Fatalf("direct base URL = %q", direct.ProviderMeta.BaseURL)
	}
	var catalogModelFound bool
	for _, model := range services.ModelRuntime().GetModels() {
		if model.ProviderMeta.ProviderID != "cloudflare-ai-gateway" || model.ID != "gpt-5.6-luna" {
			continue
		}
		catalogModelFound = true
		if model.ProviderMeta.BaseURL != direct.ProviderMeta.BaseURL {
			t.Errorf("catalog base URL = %q, direct base URL = %q", model.ProviderMeta.BaseURL, direct.ProviderMeta.BaseURL)
		}
		if model.ProviderMeta.Headers != nil || direct.ProviderMeta.Headers != nil {
			t.Errorf("catalog/direct projections exposed configured request headers: %#v / %#v", model.ProviderMeta.Headers, direct.ProviderMeta.Headers)
		}
		if model.ProviderMeta.Compat == nil || model.ProviderMeta.Compat.SupportsStrictMode == nil || *model.ProviderMeta.Compat.SupportsStrictMode {
			t.Fatalf("catalog compat = %#v, want supportsStrictMode=false", model.ProviderMeta.Compat)
		}
	}
	if !catalogModelFound {
		t.Fatal("generated model missing from full catalog")
	}
}

func TestProjectionPreservesCompleteUpstreamCompat(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"modelOverrides":{"gpt-5.4":{"compat":{"supportsFinishReason":false,"chatTemplateKwargs":{"enable_thinking":true},"vllmPriority":0,"supportsMaxOutputTokens":false}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("openai", "gpt-5.4")
	if model == nil {
		t.Fatal("configured generated model is absent")
	}
	wire, err := json.Marshal(extension.ModelInfo(model)["compat"])
	if err != nil {
		t.Fatal(err)
	}
	var compat map[string]any
	if err := json.Unmarshal(wire, &compat); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"supportsFinishReason":    false,
		"chatTemplateKwargs":      map[string]any{"enable_thinking": true},
		"vllmPriority":            float64(0),
		"supportsMaxOutputTokens": false,
	}
	for field, value := range want {
		got, ok := compat[field]
		if !ok {
			t.Errorf("projected compat is missing %q: %s", field, wire)
			continue
		}
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(value)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("compat.%s = %s, want %s", field, gotJSON, wantJSON)
		}
	}
}

func TestRegistryFindDoesNotPublishResolvedRequestHeaders(t *testing.T) {
	t.Setenv("PIG_INDEPENDENT_SECRET", "do-not-publish")
	t.Setenv("OPENAI_API_KEY", "test-key")
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	agentDir := t.TempDir()
	config := fmt.Sprintf(`{"providers":{"openai":{"baseUrl":%q,"headers":{"Authorization":"Bearer $PIG_INDEPENDENT_SECRET"}}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("openai", "gpt-5.4")
	if model == nil {
		t.Fatal("configured generated model is absent")
	}
	if got := extension.ModelInfo(model)["headers"]; got != nil {
		t.Fatalf("ModelRegistry.find projection published request headers: %#v", got)
	}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("runtime result = %#v", result)
	}
	if authorization != "Bearer do-not-publish" {
		t.Fatalf("request Authorization = %q, want resolved configured header", authorization)
	}
}

type modelOperationBridgeProbe struct {
	actions map[string]any
}

func (probe *modelOperationBridgeProbe) SetHostAction(key string, action any) {
	probe.actions[key] = action
}

func (*modelOperationBridgeProbe) PublishModelCatalog() {}

func TestSameLayerHeaderCollisionsUsePiInsertionOrder(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	requests := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests <- request.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	agentDir := t.TempDir()
	config := fmt.Sprintf(`{"providers":{"openai":{"baseUrl":%q,"headers":{"X-Provider-Dupe":"first","x-provider-dupe":"second","X-Delete-Dupe":"value","x-delete-dupe":null},"models":[{"id":"gpt-5.4","headers":{"X-Model-Dupe":"definition-first","x-model-dupe":"definition-second"}}],"modelOverrides":{"gpt-5.4":{"headers":{"X-Model-Dupe":"override-first","x-model-dupe":"override-second","X-Override-Dupe":"first","x-override-dupe":"second"}}}}}}`, server.URL)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}

	for iteration := range 512 {
		entry := services.Registry().ResolveGeneratedModel("openai", "gpt-5.4", mustGeneratedModel(t, "openai/gpt-5.4"))
		assertOneHeader(t, iteration, entry.Headers, "x-provider-dupe", "second")
		assertOneHeader(t, iteration, entry.Headers, "x-model-dupe", "definition-second")
		assertOneHeader(t, iteration, entry.Headers, "x-override-dupe", "second")
		assertNoHeader(t, iteration, entry.Headers, "x-delete-dupe")
	}

	probe := &modelOperationBridgeProbe{actions: make(map[string]any)}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	defer detach()
	result := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)(t.Context(), "openai", "gpt-5.4")
	headers := result["headers"].(map[string]string)
	assertOneHeader(t, 0, headers, "x-provider-dupe", "second")
	assertOneHeader(t, 0, headers, "x-model-dupe", "definition-second")
	assertOneHeader(t, 0, headers, "x-override-dupe", "second")
	assertNoHeader(t, 0, headers, "x-delete-dupe")

	model := services.ModelRuntime().GetModel("openai", "gpt-5.4")
	if model == nil {
		t.Fatal("generated model is absent")
	}
	terminal := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if terminal.StopReason != ai.StopReasonStop {
		t.Fatalf("terminal = %#v", terminal)
	}
	select {
	case request := <-requests:
		if request.Get("x-provider-dupe") != "second" || request.Get("x-model-dupe") != "definition-second" || request.Get("x-override-dupe") != "second" || request.Get("x-delete-dupe") != "" {
			t.Fatalf("request headers = %#v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("provider request was not captured")
	}
}

func mustGeneratedModel(t *testing.T, id string) *ai.GeneratedModel {
	t.Helper()
	model, ok := ai.LookupModelExact(id)
	if !ok {
		t.Fatalf("generated model %q is absent", id)
	}
	return model
}

func assertOneHeader(t *testing.T, iteration int, headers map[string]string, wantName, wantValue string) {
	t.Helper()
	matches := 0
	for name, value := range headers {
		if !strings.EqualFold(name, wantName) {
			continue
		}
		matches++
		if name != wantName || value != wantValue {
			t.Fatalf("iteration %d header = %q:%q, want %s:%s", iteration, name, value, wantName, wantValue)
		}
	}
	if matches != 1 {
		t.Fatalf("iteration %d header matches = %d, want 1: %#v", iteration, matches, headers)
	}
}

func assertNoHeader(t *testing.T, iteration int, headers map[string]string, absentName string) {
	t.Helper()
	for name := range headers {
		if strings.EqualFold(name, absentName) {
			t.Fatalf("iteration %d deleted header remained as %q", iteration, name)
		}
	}
}

func TestGetApiKeyAndHeadersUsesPiHeaderOrder(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"headers":{"X-Layer":"models-json-provider"},"models":[{"id":"extension-only","headers":{"X-Model":"models-json-definition"}}],"modelOverrides":{"extension-only":{"headers":{"X-Model":"models-json-override"}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("openai", extension.ProviderConfig{
		BaseURL: "https://extension.invalid/v1",
		Headers: map[string]string{"x-layer": "extension-provider"},
		Models: []extension.ProviderModelConfig{{
			ID: "extension-only", Name: "Extension", Input: []string{"text"},
			ContextWindow: 1000, MaxTokens: 100,
			Headers: map[string]string{"x-model": "extension-model"},
		}},
	})
	probe := &modelOperationBridgeProbe{actions: make(map[string]any)}
	detach := icodingagent.WireModelOperations(probe, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	defer detach()

	getModelAuth := probe.actions["getModelAuth"].(func(context.Context, string, string) map[string]any)
	result := getModelAuth(t.Context(), "openai", "extension-only")
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("getApiKeyAndHeaders result = %#v", result)
	}
	headers, ok := result["headers"].(map[string]string)
	if !ok {
		t.Fatalf("getApiKeyAndHeaders headers = %#v", result["headers"])
	}
	if headers["x-layer"] != "extension-provider" || headers["x-model"] != "extension-model" || len(headers) != 2 {
		t.Fatalf("getApiKeyAndHeaders headers = %#v", headers)
	}
}

func TestExtensionModelListPresenceControlsCatalogReplacement(t *testing.T) {
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	registry := services.Registry()

	registry.RegisterProvider("openai", extension.ProviderConfig{BaseURL: "https://extension.invalid/v1"})
	if got := services.ModelRuntime().GetModel("openai", "gpt-5.4"); got == nil {
		t.Fatal("omitted extension models removed generated membership")
	}

	registry.RegisterProvider("openai", extension.ProviderConfig{
		Models: []extension.ProviderModelConfig{},
	})
	if got := services.ModelRuntime().GetModel("openai", "gpt-5.4"); got != nil {
		t.Errorf("explicit empty extension models retained generated membership: %#v", got)
	}
	for _, model := range services.ModelRuntime().GetModels() {
		if model.ProviderMeta.ProviderID == "openai" {
			t.Fatalf("explicit empty extension models retained %q", model.ID)
		}
	}

	registry.RegisterProvider("openai", extension.ProviderConfig{
		API: ai.APIOpenAIResponses,
		Models: []extension.ProviderModelConfig{{
			ID: "extension-only", Name: "Extension only", API: ai.APIOpenAIResponses,
			Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100,
		}},
	})
	if got := services.ModelRuntime().GetModel("openai", "extension-only"); got == nil {
		t.Fatal("non-empty extension models did not publish replacement membership")
	}
	if got := services.ModelRuntime().GetModel("openai", "gpt-5.4"); got != nil {
		t.Errorf("non-empty extension models retained generated membership: %#v", got)
	}

	registry.UnregisterProvider("openai")
	if got := services.ModelRuntime().GetModel("openai", "gpt-5.4"); got == nil {
		t.Fatal("unregister did not restore generated membership")
	}
}

func TestExtensionRequestHeaderCompositionMatchesPiOrder(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requestHeaders <- request.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"headers":{"X-Provider":"models-json-provider","X-Provider-Only":"provider-only","X-Delete":"provider"},"models":[{"id":"extension-only","headers":{"X-Definition":"models-json-definition","X-Model":"models-json-definition","X-Definition-Only":"definition-only"}}],"modelOverrides":{"extension-only":{"headers":{"x-definition":"models-json-override","x-model":"models-json-override","X-Override-Only":"override-only","X-DELETE":null}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("openai", extension.ProviderConfig{
		BaseURL: server.URL,
		API:     ai.APIOpenAIResponses,
		Headers: map[string]string{"x-provider": "extension-provider", "X-Extension-Only": "extension-only", "x-delete": "extension"},
		Models: []extension.ProviderModelConfig{{
			ID: "extension-only", Name: "Extension", API: ai.APIOpenAIResponses,
			Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100,
			Headers: map[string]string{"X-MODEL": "extension-model", "X-Extension-Model-Only": "extension-model-only"},
		}},
	})

	entry, ok := services.Registry().Resolve("openai", "extension-only")
	if !ok {
		t.Fatal("extension model is absent")
	}
	want := map[string]string{
		"x-provider":             "extension-provider",
		"X-Provider-Only":        "provider-only",
		"X-Definition":           "models-json-definition",
		"X-Definition-Only":      "definition-only",
		"X-Override-Only":        "override-only",
		"X-MODEL":                "extension-model",
		"X-Extension-Only":       "extension-only",
		"X-Extension-Model-Only": "extension-model-only",
	}
	assertHeadersEqualFold(t, entry.Headers, want)
	for name := range entry.Headers {
		if strings.EqualFold(name, "X-Delete") {
			t.Errorf("nullable model override did not delete %q", name)
		}
	}

	model := services.ModelRuntime().GetModel("openai", "extension-only")
	if model == nil {
		t.Fatal("extension model lookup returned nil")
	}
	if model.ProviderMeta.Headers != nil {
		t.Fatalf("model projection exposed request headers: %#v", model.ProviderMeta.Headers)
	}
	result := services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	if result.StopReason != ai.StopReasonStop {
		t.Fatalf("runtime result = %#v", result)
	}
	select {
	case got := <-requestHeaders:
		for name, value := range want {
			if actual := got.Get(name); actual != value {
				t.Errorf("provider request header %s = %q, want %q", name, actual, value)
			}
		}
		if actual := got.Get("X-Delete"); actual != "" {
			t.Errorf("provider request X-Delete = %q, want absent", actual)
		}
	case <-time.After(time.Second):
		t.Fatal("provider request was not captured")
	}
}

func assertHeadersEqualFold(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("headers count = %d, want %d: %#v", len(got), len(want), got)
	}
	for wantName, wantValue := range want {
		matches := 0
		for gotName, gotValue := range got {
			if strings.EqualFold(gotName, wantName) {
				matches++
				if gotValue != wantValue {
					t.Errorf("header %s = %q, want %q", gotName, gotValue, wantValue)
				}
			}
		}
		if matches != 1 {
			t.Errorf("header %s case-insensitive matches = %d, want 1: %#v", wantName, matches, got)
		}
	}
}

func TestExtensionModelsReplaceBuiltinProviderCatalog(t *testing.T) {
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("openai", extension.ProviderConfig{
		BaseURL: "https://extension.invalid/v1",
		API:     ai.APIOpenAIResponses,
		Models: []extension.ProviderModelConfig{{
			ID: "extension-only", Name: "Extension only", API: ai.APIOpenAIResponses,
			Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100,
		}},
	})
	if got := services.ModelRuntime().GetModel("openai", "gpt-5.4"); got != nil {
		t.Errorf("replaced built-in model remains findable: %#v", got)
	}
	var providerModels []string
	for _, model := range services.ModelRuntime().GetModels() {
		if model.ProviderMeta.ProviderID == "openai" {
			providerModels = append(providerModels, model.ID)
		}
	}
	if len(providerModels) != 1 || providerModels[0] != "extension-only" {
		t.Errorf("extension provider catalog = %v, want [extension-only]", providerModels)
	}
}

func TestModelsJSONOverrideRemainsAboveExtensionModels(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"modelOverrides":{"extension-only":{"name":"Configured name","reasoning":false,"cost":{"input":0}}}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	services.Registry().RegisterProvider("openai", extension.ProviderConfig{
		BaseURL: "https://extension.invalid/v1",
		API:     ai.APIOpenAIResponses,
		Models: []extension.ProviderModelConfig{{
			ID: "extension-only", Name: "Extension name", API: ai.APIOpenAIResponses,
			Reasoning: true, Input: []string{"text"}, ContextWindow: 1000, MaxTokens: 100,
			Cost: extension.ProviderModelCost{Input: 9},
		}},
	})
	model := services.ModelRuntime().GetModel("openai", "extension-only")
	if model == nil {
		t.Fatal("extension model is absent")
	}
	if model.DisplayName != "Configured name" || model.ProviderMeta.Reasoning || model.Capabilities.InputCostPer1M != 0 {
		t.Errorf("composed extension model = name %q reasoning %v input cost %v", model.DisplayName, model.ProviderMeta.Reasoning, model.Capabilities.InputCostPer1M)
	}
}

func TestProviderValidationAcceptsRequestAuthOnlyConfiguration(t *testing.T) {
	for name, provider := range map[string]string{
		"headers":    `{"headers":{"X-Test":"yes"}}`,
		"apiKey":     `{"apiKey":"configured"}`,
		"authHeader": `{"authHeader":false}`,
	} {
		t.Run(name, func(t *testing.T) {
			agentDir := t.TempDir()
			config := `{"providers":{"openai":` + provider + `}}`
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			if loadError := services.Registry().LoadError(); loadError != "" {
				t.Fatalf("load error = %q", loadError)
			}
			if model := services.ModelRuntime().GetModel("openai", "gpt-5.4"); model == nil {
				t.Fatal("generated model is absent")
			}
		})
	}
}

// Upstream applyModelsJson rejects "oauth" without "baseUrl"; the runtime
// reports the composition error and keeps the built-in provider.
func TestOAuthWithoutBaseURLKeepsBuiltinProviderAndReportsError(t *testing.T) {
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(`{"providers":{"openai":{"oauth":"radius"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if want := `Provider "openai": Provider openai: "baseUrl" is required when "oauth" is set.`; services.Registry().LoadError() != want {
		t.Fatalf("load error = %q, want %q", services.Registry().LoadError(), want)
	}
	if model := services.ModelRuntime().GetModel("openai", "gpt-5.4"); model == nil {
		t.Fatal("built-in openai model is absent after the composition error")
	}
}

func TestHeadersOnlyProviderOverlayIsValid(t *testing.T) {
	agentDir := t.TempDir()
	config := `{"providers":{"openai":{"headers":{"X-Configured":"yes"}}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("openai", "gpt-5.4")
	if model == nil {
		t.Fatal("generated model is absent")
	}
	if got := extension.ModelInfo(model)["headers"]; got != nil {
		t.Fatalf("model projection exposed configured request headers: %#v", got)
	}
	entry, ok := services.Registry().Resolve("openai", "gpt-5.4")
	if !ok || entry.Headers["X-Configured"] != "yes" {
		t.Fatalf("resolved request headers = %#v, ok=%v", entry.Headers, ok)
	}
	for _, candidate := range services.ModelRuntime().GetModels() {
		if candidate.ProviderMeta.ProviderID == "openai" && candidate.ID == "gpt-5.4" {
			if got := extension.ModelInfo(candidate)["headers"]; got != nil {
				t.Errorf("catalog projection exposed configured request headers: %#v", got)
			}
			return
		}
	}
	t.Fatal("generated model is absent from catalog")
}
