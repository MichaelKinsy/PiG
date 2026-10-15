package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// google.ts convertMessages keeps a thinking block's signature, and sends it as a thought, when the earlier assistant turn came from the same provider,
// API and model (transform-messages.ts:46-49). A provider built from a bare model id, with no catalog metadata, still names its own API, so its own
// turns are the same model: the wire keeps `thought: true` and the `thoughtSignature` (the pinned-Pi provider-wire probe sends exactly this).
func TestGoogleProviderKeepsItsOwnThinkingSignaturesWithoutCatalogMetadata(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		body = string(raw)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{BaseURL: server.URL + "/v1beta", APIKey: "k", Model: "gemini-3-pro", ProviderID: "google"})
	transcript := NormalizeContext(Context{Messages: []Message{
		AssistantMessage{Provider: "google", API: APIGoogleGenerativeAI, Model: "gemini-3-pro", Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "reasoning", ThinkingSignature: "c2lnbmF0dXJl"},
			TextContent{Text: "answer"},
		}},
		UserMessage{Content: UserText("next")},
	}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err == nil {
		stream.Result()
	}
	if !strings.Contains(body, `"thought":true`) || !strings.Contains(body, `"thoughtSignature":"c2lnbmF0dXJl"`) {
		t.Fatalf("request body = %s; want the same-model thinking block sent as a thought with its signature", body)
	}
	// Another model's thinking is dropped (Pi #11000).
	body = ""
	transcript = NormalizeContext(Context{Messages: []Message{
		AssistantMessage{Provider: "google", API: APIGoogleGenerativeAI, Model: "gemini-2.5-pro", Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "reasoning", ThinkingSignature: "c2lnbmF0dXJl"},
		}},
		UserMessage{Content: UserText("next")},
	}})
	if stream, err = provider.Stream(context.Background(), transcript, StreamOptions{}); err == nil {
		stream.Result()
	}
	if strings.Contains(body, "thoughtSignature") || strings.Contains(body, "reasoning") {
		t.Fatalf("request body = %s; want another model's thinking dropped", body)
	}
}
