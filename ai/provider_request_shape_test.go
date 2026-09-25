package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// captureShapeRequest exercises the public provider boundary and the actual JSON encoder.
func captureShapeRequest(t *testing.T, factory func(string) Provider, messages []Message, opts StreamOptions, reply string) map[string]json.RawMessage {
	t.Helper()
	requests := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reply)
	}))
	defer server.Close()
	provider := factory(server.URL)
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages}), opts)
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
	select {
	case body := <-requests:
		return body
	default:
		t.Fatal("provider completed without an HTTP request")
		return nil
	}
}

func assertShapeJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("request shape\n got: %s\nwant: %s", got, want)
	}
}

// openai-responses-shared.ts convertResponsesMessages maps every text block, including
// empty text. Empty content arrays alone are skipped; empty blocks still consume IDs.
func TestResponsesRequestPreservesEmptyText(t *testing.T) {
	for _, providerID := range []string{"openai", "github-copilot"} {
		t.Run(providerID, func(t *testing.T) {
			body := captureShapeRequest(t, func(url string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "test", ProviderID: providerID, Model: "test"})
			}, []Message{
				UserMessage{Content: UserText("")},
				UserMessage{Content: UserText(" \t")},
				UserMessage{Content: UserContentBlocks{}},
				UserMessage{Content: UserContentBlocks{TextContent{}, TextContent{Text: "hello"}, ImageContent{MimeType: "image/png", Data: "aGk="}, TextContent{}}},
				AssistantMessage{Provider: providerID, API: APIOpenAIResponses, Model: "test", StopReason: StopReasonStop, Content: []AssistantContentBlock{
					TextContent{}, TextContent{Text: "answer"}, TextContent{TextSignature: `{"v":1,"id":"signed","phase":"final_answer"}`},
				}},
			}, StreamOptions{}, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			assertShapeJSON(t, body["input"], `[
				{"role":"user","content":[{"type":"input_text","text":""}]},
				{"role":"user","content":[{"type":"input_text","text":" \t"}]},
				{"role":"user","content":[{"type":"input_text","text":""},{"type":"input_text","text":"hello"},{"type":"input_image","detail":"auto","image_url":"data:image/png;base64,aGk="},{"type":"input_text","text":""}]},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed","id":"msg_pi_3"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}],"status":"completed","id":"msg_pi_3_1"},
				{"type":"message","role":"assistant","content":[{"type":"output_text","text":"","annotations":[]}],"status":"completed","id":"signed","phase":"final_answer"}
			]`)
		})
	}
}

// Empty arrays are omitted without consuming the Responses fallback message ID.
func TestResponsesRequestEmptyContentDoesNotConsumeMessageID(t *testing.T) {
	body := captureShapeRequest(t, func(url string) Provider {
		return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "test", ProviderID: "openai", Model: "test"})
	}, []Message{
		UserMessage{Content: UserContentBlocks{}},
		AssistantMessage{Content: []AssistantContentBlock{}},
		AssistantMessage{},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "answer"}}},
	}, StreamOptions{}, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
	assertShapeJSON(t, body["input"], `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}],"status":"completed","id":"msg_pi_0"}]`)
}

// google-shared.ts preserves all user text, but only signed empty assistant text/thinking.
// Non-text parts must not acquire a text field when preserving empty text on the wire.
func TestGoogleRequestPreservesEmptyText(t *testing.T) {
	for _, providerID := range []string{"google", "google-vertex"} {
		t.Run(providerID, func(t *testing.T) {
			body := captureShapeRequest(t, func(url string) Provider {
				if providerID == "google-vertex" {
					return NewGoogleVertexProvider(GoogleVertexConfig{BaseURL: url, APIKey: "test", Model: "test", ProviderID: providerID})
				}
				return NewGoogleProvider(GoogleConfig{BaseURL: url, APIKey: "test", Model: "test", ProviderID: providerID})
			}, []Message{
				UserMessage{Content: UserText("")},
				UserMessage{Content: UserText(" \t")},
				UserMessage{Content: UserContentBlocks{}},
				UserMessage{Content: UserContentBlocks{TextContent{}, TextContent{Text: " \t"}, TextContent{Text: "hello"}, ImageContent{MimeType: "image/png", Data: "aGk="}}},
				AssistantMessage{Provider: providerID, Model: "test", StopReason: StopReasonStop, Content: []AssistantContentBlock{
					TextContent{}, TextContent{Text: " \t"}, ThinkingContent{},
					TextContent{TextSignature: "c2ln"}, ThinkingContent{ThinkingSignature: "c2ln"}, TextContent{Text: "answer"},
				}},
			}, StreamOptions{}, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
			assertShapeJSON(t, body["contents"], `[
				{"role":"user","parts":[{"text":""}]},
				{"role":"user","parts":[{"text":" \t"}]},
				{"role":"user","parts":[{"text":""},{"text":" \t"},{"text":"hello"},{"inlineData":{"mimeType":"image/png","data":"aGk="}}]},
				{"role":"model","parts":[{"text":"","thoughtSignature":"c2ln"},{"text":"","thought":true,"thoughtSignature":"c2ln"},{"text":"answer"}]}
			]`)
		})
	}
}
