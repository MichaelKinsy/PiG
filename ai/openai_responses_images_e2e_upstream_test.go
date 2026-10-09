package ai

// The PNG fixture is copied from .upstream/v0.87.1/packages/ai/test/data/red-circle.png (SHA-256 8a0661c7398b69a4051a47733b6ed5540b23549f2245ba2d608fd9d050b7e919).

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const upstreamCircleText = "A red circle with a diameter of 100 pixels."

type responsesImageTestCase struct {
	provider, model, keyEnv string
}

func responsesImageCases() []responsesImageTestCase {
	return []responsesImageTestCase{
		// .upstream/v0.87.1/packages/ai/test/openai-responses-tool-result-images.test.ts:150
		{"openai", "gpt-5-mini", "PIG_LIVE_OPENAI_API_KEY"},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-tool-result-images.test.ts:160
		{"azure", "gpt-4o-mini", "PIG_LIVE_AZURE_OPENAI_API_KEY"},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-tool-result-images.test.ts:168
		{"github-copilot", "gpt-5-mini", "PIG_LIVE_COPILOT_TOKEN"},
		// .upstream/v0.87.1/packages/ai/test/openai-responses-tool-result-images.test.ts:183
		{"openai-codex", "gpt-5.5", "PIG_LIVE_CODEX_TOKEN"},
	}
}

func TestOpenAIResponsesToolResultImagesUpstream(t *testing.T) {
	for _, tc := range responsesImageCases() {
		t.Run(tc.provider+"/should send tool result images in function_call_output", func(t *testing.T) {
			t.Run("hermetic", func(t *testing.T) {
				image, err := os.ReadFile("testdata/upstream-red-circle.png")
				if err != nil {
					t.Fatal(err)
				}
				encoded := base64.StdEncoding.EncodeToString(image)
				url := responsesImageFauxEndpoint(t, tc.provider == "openai-codex", encoded, tc.model)
				key := "test"
				if tc.provider == "openai-codex" {
					key = codexTestToken(t, "acct_image")
				}
				runResponsesImageCase(t, t.Context(), tc.provider, tc.model, key, url, encoded, true)
			})
		})
	}
}

func TestAzureDeploymentPreservesLogicalModelAndImageReplay(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4o-mini=image-deployment")
	image, err := os.ReadFile("testdata/upstream-red-circle.png")
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(image)
	baseURL := responsesImageFauxEndpoint(t, false, encoded, "image-deployment")
	runResponsesImageCase(t, t.Context(), "azure", "gpt-4o-mini", "test", baseURL, encoded, true)
}

func responsesImageProvider(t *testing.T, providerID, model, key, baseURL string) Provider {
	t.Helper()
	switch providerID {
	case "azure":
		return NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: key, Model: model, BaseURL: baseURL})
	case "openai-codex":
		return NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: key, Model: model, BaseURL: baseURL})
	case "github-copilot":
		auth, err := NewAuthStorage(filepath.Join(t.TempDir(), "auth.json"))
		if err != nil {
			t.Fatal(err)
		}
		provider, err := NewCopilotProvider(CopilotProviderConfig{Auth: auth, Model: model, API: APIOpenAIResponses, Reasoning: true, RuntimeToken: func() (string, bool) { return key, true }})
		if err != nil {
			t.Fatal(err)
		}
		if baseURL != "" {
			p := provider.(*openAIResponsesProvider)
			p.cfg.GetBaseURL = nil
			p.cfg.BaseURL = baseURL
		}
		return provider
	default:
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: key, Model: model, ProviderID: providerID, BaseURL: baseURL, IsReasoning: true})
	}
}

func runResponsesImageCase(t *testing.T, ctx context.Context, providerID, model, key, baseURL, image string, hermetic bool) {
	t.Helper()
	provider := responsesImageProvider(t, providerID, model, key, baseURL)
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	options := StreamOptions{}
	if providerID != "azure" {
		options.Thinking = ThinkingLevelLow
		options.IsReasoning = true
	}
	if hermetic && providerID == "openai-codex" {
		options.Transport = TransportSSE
	}
	request := Context{SystemPrompt: "You are a helpful assistant that always uses the provided tool when asked.", Messages: []Message{UserMessage{Content: UserText("Call get_circle_with_description, then describe both the tool text and the image. Mention the color and shape.")}}, Tools: []ToolSchema{{Name: "get_circle_with_description", Description: "Returns a red circle image with a short text description.", Parameters: JsonObject{"type": "object", "properties": JsonObject{}}}}}
	first, err := provider.Stream(ctx, NormalizeContext(request), options)
	if err != nil {
		t.Fatal(err)
	}
	assistant := first.Result()
	if assistant.StopReason != StopReasonToolUse {
		t.Fatalf("first response=%#v", assistant)
	}
	if assistant.Model != model {
		t.Fatalf("logical model=%q, want %q", assistant.Model, model)
	}
	var call *ToolCall
	for _, block := range assistant.Content {
		if tool, ok := block.(ToolCall); ok {
			call = &tool
			break
		}
	}
	if call == nil {
		t.Fatal("missing tool call")
	}
	request.Messages = append(request.Messages, *assistant, ToolResultMessage{ToolCallID: call.ID, ToolName: call.Name, Content: []ToolResultMessageContent{TextContent{Text: upstreamCircleText}, ImageContent{MimeType: "image/png", Data: image}}})
	var captured struct {
		Input []map[string]json.RawMessage `json:"input"`
	}
	options.OnPayload = func(payload any, _ *Model) (any, error) {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return nil, json.Unmarshal(data, &captured)
	}
	second, err := provider.Stream(ctx, NormalizeContext(request), options)
	if err != nil {
		t.Fatal(err)
	}
	response := second.Result()
	if response.StopReason != StopReasonStop || response.ErrorMessage != "" {
		t.Fatalf("second response=%#v", response)
	}
	found := -1
	for i, item := range captured.Input {
		if string(item["type"]) == `"function_call_output"` {
			found = i
			break
		}
	}
	if found < 0 {
		t.Fatalf("missing function_call_output: %#v", captured.Input)
	}
	var output []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"image_url"`
	}
	if err := json.Unmarshal(captured.Input[found]["output"], &output); err != nil {
		t.Fatal(err)
	}
	textOK, imageOK := false, false
	for _, item := range output {
		if item.Type == "input_text" && strings.Contains(item.Text, upstreamCircleText) {
			textOK = true
		}
		if item.Type == "input_image" && strings.HasPrefix(item.ImageURL, "data:image/png;base64,") {
			imageOK = true
		}
	}
	if !textOK || !imageOK {
		t.Fatalf("output=%#v", output)
	}
	for _, item := range captured.Input[found+1:] {
		if string(item["role"]) == `"user"` {
			t.Fatal("image escaped into a later user turn")
		}
	}
	var text strings.Builder
	for _, block := range response.Content {
		if block, ok := block.(TextContent); ok {
			text.WriteString(block.Text)
			text.WriteByte(' ')
		}
	}
	lower := strings.ToLower(text.String())
	if !strings.Contains(lower, "red") || !strings.Contains(lower, "circle") {
		t.Fatalf("response text=%q", text.String())
	}
}

func responsesImageFauxEndpoint(t *testing.T, codex bool, image, requestModel string) string {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if codex {
			data, err = decodeZstdRawFrameForTest(data)
			if err != nil {
				t.Error(err)
				http.Error(w, err.Error(), 400)
				return
			}
		}
		var payload struct {
			Model string                       `json:"model"`
			Input []map[string]json.RawMessage `json:"input"`
			Tools []struct {
				Type        string                     `json:"type"`
				Name        string                     `json:"name"`
				Description string                     `json:"description"`
				Parameters  map[string]json.RawMessage `json:"parameters"`
			} `json:"tools"`
		}
		if err = json.Unmarshal(data, &payload); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 400)
			return
		}
		if payload.Model != requestModel {
			t.Errorf("request model=%q, want %q", payload.Model, requestModel)
			w.WriteHeader(400)
			return
		}
		turn := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if turn == 1 {
			// upstream: packages/ai/test/openai-responses-tool-result-images.test.ts:21-26,48-62 keeps the complete user prompt and tool schema.
			var prompts []string
			for _, input := range payload.Input {
				if string(input["role"]) != `"user"` {
					continue
				}
				var content []struct{ Type, Text string }
				if err := json.Unmarshal(input["content"], &content); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				for _, block := range content {
					if block.Type == "input_text" {
						prompts = append(prompts, block.Text)
					}
				}
			}
			wantPrompt := "Call get_circle_with_description, then describe both the tool text and the image. Mention the color and shape."
			if len(prompts) != 1 || prompts[0] != wantPrompt || len(payload.Tools) != 1 {
				t.Errorf("changed image request: prompts=%#v tools=%#v", prompts, payload.Tools)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			tool := payload.Tools[0]
			if tool.Type != "function" || tool.Name != "get_circle_with_description" || tool.Description != "Returns a red circle image with a short text description." || string(tool.Parameters["type"]) != `"object"` || string(tool.Parameters["properties"]) != `{}` {
				t.Errorf("changed upstream tool schema: %#v", tool)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_circle\",\"call_id\":\"call_circle\",\"name\":\"get_circle_with_description\",\"arguments\":\"\"}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_circle\",\"call_id\":\"call_circle\",\"name\":\"get_circle_with_description\",\"arguments\":\"{}\"}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			return
		}
		if turn != 2 {
			t.Errorf("unexpected request %d", turn)
			w.WriteHeader(400)
			return
		}
		found := -1
		for i, item := range payload.Input {
			if string(item["type"]) == `"function_call_output"` {
				found = i
				break
			}
		}
		if found < 0 {
			t.Error("missing tool output")
			w.WriteHeader(400)
			return
		}
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL string `json:"image_url"`
		}
		if err = json.Unmarshal(payload.Input[found]["output"], &parts); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		textOK, imageOK := false, false
		for _, part := range parts {
			if part.Type == "input_text" && part.Text == upstreamCircleText {
				textOK = true
			}
			if part.Type == "input_image" && part.ImageURL == "data:image/png;base64,"+image {
				imageOK = true
			}
		}
		if !textOK || !imageOK {
			t.Error("tool image/text missing or changed")
			w.WriteHeader(400)
			return
		}
		for _, item := range payload.Input[found+1:] {
			if string(item["role"]) == `"user"` {
				t.Error("later user image turn")
				w.WriteHeader(400)
				return
			}
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_circle\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_circle\",\"content\":[{\"type\":\"output_text\",\"text\":\"A red circle.\"}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	}))
	t.Cleanup(server.Close)
	return server.URL
}
