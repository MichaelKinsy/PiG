package codingagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type modelRequestCaptureHandle struct {
	*recordingCompactHandle
	model   *ai.Model
	request ai.Context
	options ai.StreamOptions
}

func (handle *modelRequestCaptureHandle) StreamModel(_ context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	handle.model = model
	handle.request = request
	handle.options = options
	return completedTestStream("capture")
}

func TestStreamForSubprocessPreservesFieldRichContextAndOptions(t *testing.T) {
	base := &recordingCompactHandle{}
	handle := &modelRequestCaptureHandle{recordingCompactHandle: base}
	model := &ai.Model{ID: "model", ProviderMeta: ai.ProviderMetadata{ProviderID: "provider"}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
	mode := &InteractiveMode{opts: InteractiveOptions{
		SessionHandle: handle,
		ModelBuilder:  func(string) (*ai.Model, error) { return model, nil },
		Settings:      Settings{Transport: string(ai.TransportWebSocket)},
	}}
	endTurn := true
	request := map[string]any{
		"systemPrompt": "root prompt",
		"tools": []any{map[string]any{
			"name": "top", "description": "top tool", "parameters": map[string]any{"type": "object"},
			"promptGuidelines":    []any{"use top"},
			"constrainedSampling": map[string]any{"type": "grammar", "variants": map[string]any{"openai_lark": "start: NUMBER"}},
		}},
		"messages": []any{
			map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "update", "textSignature": "system-signature"}}, "sections": ai.OrderedSections{{Name: "zeta", Value: new("last-first")}, {Name: "alpha", Value: nil}, {Name: "middle", Value: new("middle")}}, "toolsAdded": []any{map[string]any{"name": "added", "description": "added tool", "parameters": map[string]any{"type": "object"}, "constrainedSampling": map[string]any{"type": "json_schema", "strict": "require"}}}, "toolsRemoved": []any{map[string]any{"name": "old"}}, "timestamp": float64(11)},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hello", "textSignature": "user-signature"}, map[string]any{"type": "image", "data": "aW1n", "mimeType": "image/png"}}, "timestamp": float64(12)},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "answer", "textSignature": "text-signature"}, map[string]any{"type": "thinking", "thinking": "thought", "thinkingSignature": "thinking-signature", "redacted": true}, map[string]any{"type": "toolCall", "id": "call", "name": "read", "arguments": map[string]any{"path": "x"}, "thoughtSignature": "tool-signature", "namespace": "ns"}}, "api": "openai-responses", "provider": "wire-provider", "model": "wire-model", "responseModel": "response-model", "responseId": "response-id", "providerThinkingLevel": "high", "diagnostics": []any{map[string]any{"type": "warning", "timestamp": float64(13), "details": map[string]any{"retry": true}}}, "usage": map[string]any{"input": float64(1), "output": float64(2), "cacheRead": float64(3), "cacheWrite": float64(4), "cacheWrite1h": float64(5), "reasoning": float64(6), "totalTokens": float64(15), "cost": map[string]any{"input": 0.1, "output": 0.2, "cacheRead": 0.3, "cacheWrite": 0.4, "total": 1.0}}, "stopReason": "toolUse", "deferred": map[string]any{"provider": "deferred-provider", "modelId": "deferred-model", "api": "openai-responses", "id": "deferred-id"}, "rawStopReason": "raw", "endTurn": endTurn, "timestamp": float64(14)},
			map[string]any{"role": "toolResult", "toolCallId": "call", "toolName": "read", "content": []any{map[string]any{"type": "text", "text": "result"}, map[string]any{"type": "image", "data": "cmVzdWx0", "mimeType": "image/png"}}, "details": map[string]any{"exit": float64(0)}, "usage": map[string]any{"input": float64(7), "output": float64(8), "totalTokens": float64(15), "cost": map[string]any{}}, "isError": true, "timestamp": float64(15)},
		},
		"maxTokens": float64(2048), "temperature": 0.7,
		"samplingParams":  map[string]any{"topP": 0.8},
		"thinkingBudgets": map[string]any{"minimal": float64(101), "low": float64(202), "medium": float64(303), "high": float64(404)},
		"thinking":        "high", "isReasoning": true,
		"env":       map[string]any{"WIRE_ENV": "env-value", "SECOND_ENV": "distinct-value"},
		"headers":   map[string]any{"X-Wire": "header-value", "X-Remove": nil},
		"sessionId": "session-value", "transport": "sse",
	}
	stream, err := mode.streamForSubprocess(context.Background(), map[string]any{"provider": "provider", "modelId": "model"}, request)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Result().StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", stream.Result())
	}
	if handle.model != model {
		t.Fatalf("model = %p, want %p", handle.model, model)
	}
	if handle.request.SystemPrompt != "root prompt" || len(handle.request.Tools) != 1 || len(handle.request.Messages) != 4 {
		t.Fatalf("context = %#v", handle.request)
	}
	system := handle.request.Messages[0].(ai.SystemMessage)
	if system.Timestamp != 11 || len(system.ToolsAdded) != 1 || len(system.ToolsRemoved) != 1 || !reflect.DeepEqual(system.Sections, ai.OrderedSections{{Name: "zeta", Value: new("last-first")}, {Name: "alpha", Value: nil}, {Name: "middle", Value: new("middle")}}) {
		t.Fatalf("system = %#v", system)
	}
	blocks := system.Content.(ai.SystemTextBlocks)
	if len(blocks) != 1 || blocks[0].TextSignature != "system-signature" {
		t.Fatalf("system content = %#v", system.Content)
	}
	if handle.request.Tools[0].ConstrainedSampling == nil || handle.request.Tools[0].ConstrainedSampling.Variants[ai.GrammarFormatOpenAILark] != "start: NUMBER" || system.ToolsAdded[0].ConstrainedSampling == nil {
		t.Fatalf("tools = %#v system tools = %#v", handle.request.Tools, system.ToolsAdded)
	}
	user := handle.request.Messages[1].(ai.UserMessage)
	if user.Timestamp != 12 || user.Content.(ai.UserContentBlocks)[0].(ai.TextContent).TextSignature != "user-signature" {
		t.Fatalf("user = %#v", user)
	}
	assistant := handle.request.Messages[2].(ai.AssistantMessage)
	if assistant.API != ai.APIOpenAIResponses || assistant.Provider != "wire-provider" || assistant.Model != "wire-model" || assistant.ResponseID != "response-id" || assistant.Usage.CacheWrite1h == nil || *assistant.Usage.CacheWrite1h != 5 || assistant.EndTurn == nil || !*assistant.EndTurn {
		t.Fatalf("assistant = %#v", assistant)
	}
	tool := assistant.Content[2].(ai.ToolCall)
	if tool.ThoughtSignature != "tool-signature" || tool.Namespace != "ns" || assistant.Content[1].(ai.ThinkingContent).Redacted != true {
		t.Fatalf("assistant content = %#v", assistant.Content)
	}
	result := handle.request.Messages[3].(ai.ToolResultMessage)
	if result.Timestamp != 15 || result.Details == nil || result.Usage == nil || !result.IsError || len(result.Content) != 2 {
		t.Fatalf("tool result = %#v", result)
	}
	wantOptions := ai.StreamOptions{MaxTokens: 2048, Temperature: 0.7, TemperatureSet: true, SamplingParams: map[string]any{"topP": 0.8}, ThinkingBudgets: &ai.ThinkingBudgets{Minimal: 101, Low: 202, Medium: 303, High: 404}, Thinking: ai.ThinkingHigh, IsReasoning: true, Env: ai.ProviderEnv{"WIRE_ENV": "env-value", "SECOND_ENV": "distinct-value"}, Headers: ai.ProviderHeaders{"X-Wire": new("header-value"), "X-Remove": nil}, SessionID: "session-value", Transport: ai.TransportSSE}
	if !reflect.DeepEqual(handle.options, wantOptions) {
		t.Fatalf("options = %#v, want %#v", handle.options, wantOptions)
	}
}

type providerForwardingHandle struct{ *recordingCompactHandle }

func (handle *providerForwardingHandle) StreamModel(ctx context.Context, model *ai.Model, request ai.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
	stream, err := model.Provider.Stream(ctx, ai.NormalizeContext(request), options)
	if err != nil {
		panic(err)
	}
	return stream
}

func TestStreamForSubprocessFieldRichRequestReachesRealProviderWire(t *testing.T) {
	var headers http.Header
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headers = request.Header.Clone()
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, Model: "wire-model", ProviderID: "wire", Compat: &ai.OpenAICompat{SendSessionAffinityHeaders: new(true), SupportsLongCacheRetention: new(true)}})
	model := &ai.Model{ID: "wire-model", Provider: provider}
	mode := &InteractiveMode{opts: InteractiveOptions{SessionHandle: &providerForwardingHandle{recordingCompactHandle: &recordingCompactHandle{}}, ModelBuilder: func(string) (*ai.Model, error) { return model, nil }}}
	request := map[string]any{
		"systemPrompt": "wire system", "messages": []any{map[string]any{"role": "user", "content": "wire user", "timestamp": float64(7)}},
		"tools":       []any{map[string]any{"name": "wire_tool", "description": "wire tool", "parameters": map[string]any{"type": "object"}}},
		"temperature": 0.65, "maxTokens": float64(321), "headers": map[string]any{"X-Subprocess": "preserved"},
		"env": map[string]any{"PI_CACHE_RETENTION": "long"}, "sessionId": "wire-session", "transport": "sse",
	}
	stream, err := mode.streamForSubprocess(context.Background(), map[string]any{"provider": "wire", "modelId": "wire-model"}, request)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != ai.StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	if headers.Get("X-Subprocess") != "preserved" {
		t.Fatalf("headers = %#v", headers)
	}
	if body["temperature"] != 0.65 || body["max_completion_tokens"] != float64(321) || body["prompt_cache_retention"] != "24h" {
		t.Fatalf("body = %#v", body)
	}
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
		t.Fatalf("wire tools = %#v", body["tools"])
	}
}

func TestInteractiveModelProjectionPreservesPiModelShape(t *testing.T) {
	strict := false
	model := &ai.Model{
		ID: "org/model/name", DisplayName: "Configured slash model",
		Capabilities:     ai.ModelCapabilities{SupportsImages: true, ContextWindow: 321000, MaxOutputTokens: 1234, InputCostPer1M: 0, OutputCostPer1M: 7, CacheReadCostPer1M: 0.5, CacheWriteCostPer1M: 1.5, CostTiers: []ai.CostTier{{InputTokensAbove: 1000, InputCostPer1M: 1, OutputCostPer1M: 2}}},
		ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingHigh: new("configured-high")},
		SamplingParams:   map[string]any{"temperature": float64(0)}, PromptCache: ai.ModelPromptCache{"short": 120, "long": 3600},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "openrouter", API: ai.APIOpenAIResponses, BaseURL: "https://proxy.invalid/v1", Headers: map[string]string{"X-Test": "value"}, Compat: &ai.OpenAICompat{SupportsStrictMode: &strict}},
	}
	bridge := &captureUIBridge{}
	mode := &InteractiveMode{opts: InteractiveOptions{Model: model, ModelLookup: func(string, string) *ai.Model { return model }, ModelCatalog: func() []*ai.Model { return []*ai.Model{model} }, SubprocessUIBridge: bridge}}
	detach := mode.wireSubprocessHostCallbacks()
	defer detach()
	getModel := bridge.actions["getModel"].(func(string, string) map[string]any)
	got := getModel("openrouter", "org/model/name")
	if got["id"] != model.ID || got["modelId"] != model.ID {
		t.Errorf("serialized identity = id:%v modelId:%v, want %q", got["id"], got["modelId"], model.ID)
	}
	if got["baseUrl"] != model.ProviderMeta.BaseURL {
		t.Errorf("serialized baseUrl = %#v", got["baseUrl"])
	}
	if !reflect.DeepEqual(got["input"], []string{"text", "image"}) {
		t.Errorf("serialized input = %#v", got["input"])
	}
	wantCost := map[string]any{"input": float64(0), "output": float64(7), "cacheRead": float64(0.5), "cacheWrite": float64(1.5), "tiers": model.Capabilities.CostTiers}
	if !reflect.DeepEqual(got["cost"], wantCost) {
		t.Errorf("serialized cost = %#v, want %#v", got["cost"], wantCost)
	}
	for _, field := range []string{"thinkingLevelMap", "samplingParams", "promptCache", "headers", "compat"} {
		if got[field] == nil {
			t.Errorf("serialized model is missing %q: %#v", field, got)
		}
	}
}

func TestInteractiveModelProjectionDoesNotRestoreGeneratedValues(t *testing.T) {
	model := &ai.Model{ID: "gpt-5.6-luna", DisplayName: "Configured Luna", Capabilities: ai.ModelCapabilities{InputCostPer1M: 0, CacheReadCostPer1M: 9, CacheWriteCostPer1M: 10}, ProviderMeta: ai.ProviderMetadata{ProviderID: "cloudflare-ai-gateway", Reasoning: false}}
	bridge := &captureUIBridge{}
	mode := &InteractiveMode{opts: InteractiveOptions{Model: model, SubprocessUIBridge: bridge}}
	detach := mode.wireSubprocessHostCallbacks()
	defer detach()
	getModelInfo := bridge.actions["getModelInfo"].(func() map[string]any)
	got := getModelInfo()
	if got["reasoning"] != false {
		t.Errorf("serialized reasoning = %#v, want configured false", got["reasoning"])
	}
	if got["cacheReadCostPer1M"] != float64(9) || got["cacheWriteCostPer1M"] != float64(10) {
		t.Errorf("serialized cache costs = %#v/%#v, want configured 9/10", got["cacheReadCostPer1M"], got["cacheWriteCostPer1M"])
	}
}

func TestStreamForSubprocessRejectsMissingAndMalformedFields(t *testing.T) {
	mode := &InteractiveMode{opts: InteractiveOptions{SessionHandle: &modelRequestCaptureHandle{recordingCompactHandle: &recordingCompactHandle{}}, ModelBuilder: func(string) (*ai.Model, error) { return &ai.Model{ID: "model"}, nil }}}
	for _, request := range []map[string]any{
		{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"content": []any{
						map[string]any{"type": "toolCall", "id": nil, "name": "read", "arguments": map[string]any{}},
					},
				},
			},
		},
		{"messages": []any{}, "temperature": "hot"},
		{"messages": []any{}, "headers": map[string]any{"X-Test": float64(1)}},
	} {
		if _, err := mode.streamForSubprocess(context.Background(), map[string]any{"provider": "provider", "modelId": "model"}, request); err == nil {
			t.Fatalf("request %#v was accepted", request)
		}
	}
}
