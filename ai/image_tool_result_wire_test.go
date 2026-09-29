package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeneratedModelToModelPreservesCapabilities(t *testing.T) {
	generated := &GeneratedModel{ID: "vision", Provider: "example", Capabilities: []string{"text", "image"}, ContextWindow: 128000, MaxOutputTokens: 8192, InputCostPerMTokens: 1, OutputCostPerMTokens: 2, CacheReadCost: 0.1, CacheWriteCost: 0.2}
	model := generated.ToModel()
	caps := model.Capabilities
	if !caps.SupportsImages || !caps.SupportsToolUse || caps.ContextWindow != 128000 || caps.MaxOutputTokens != 8192 || caps.InputCostPer1M != 1 || caps.OutputCostPer1M != 2 || caps.CacheReadCostPer1M != 0.1 || caps.CacheWriteCostPer1M != 0.2 {
		t.Fatalf("capabilities=%+v", caps)
	}
}

// Pi mistral-conversations.ts:413-424 maps the SDK-style imageUrl only at the HTTP boundary.
func TestMistralImageToolResultWireKeys(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		requests <- payload
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	provider := NewMistralProvider(MistralConfig{APIKey: "test", Model: "pixtral-12b", ProviderID: "mistral", BaseURL: server.URL})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{ToolResultMessage{ToolCallID: "imagecall", ToolName: "get_circle", Content: []ToolResultMessageContent{ImageContent{Data: "YWJj", MimeType: "image/png"}}}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatal(result.ErrorMessage)
	}
	payload := <-requests
	content := payload["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content=%#v", content)
	}
	image := content[1].(map[string]any)
	if image["image_url"] != "data:image/png;base64,YWJj" || image["imageUrl"] != nil {
		t.Fatalf("image wire chunk=%#v", image)
	}
}
