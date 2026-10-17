package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Pi builds every request body with JSON.stringify (the OpenAI, Anthropic, Google and Mistral SDKs call it), which writes <, > and & literally. Go's json
// encoder writes \u003c, \u003e and \u0026 unless SetEscapeHTML(false) is set, so a message carrying markup (the compaction summary's <summary> tags, a
// quoted file with generics) reached the provider with different bytes than Pi sends: equal as JSON, different on the wire.
func TestProviderRequestBodiesWriteMarkupLiterally(t *testing.T) {
	const text = "<summary>a & b</summary>"
	cases := []struct {
		name     string
		streams  func() *ProviderStreams
		api      API
		provider string
		path     string
	}{
		{"openai-completions", OpenAICompletionsAPI, APIOpenAICompletions, "openai", ""},
		{"openai-responses", OpenAIResponsesAPI, APIOpenAIResponses, "openai", ""},
		{"azure-openai-responses", AzureOpenAIResponsesAPI, APIAzureOpenAIResponses, "azure-openai-responses", "/openai/v1"},
		{"anthropic-messages", AnthropicMessagesAPI, APIAnthropicMessages, "anthropic", ""},
		{"google-generative-ai", GoogleGenerativeAIAPI, APIGoogleGenerativeAI, "google", "/v1beta"},
		{"mistral-conversations", MistralConversationsAPI, APIMistralConversations, "mistral", ""},
		{"pi-messages", PiMessagesAPI, APIPiMessages, "radius", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var body string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				if body == "" {
					body = string(raw)
				}
				mu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			model := &Model{ID: "m1", DisplayName: "m1", ProviderMeta: ProviderMetadata{API: c.api, ProviderID: c.provider, BaseURL: server.URL + c.path}, Input: []string{"text"}}
			transcript := NormalizeContext(Context{SystemPrompt: "<role>helper</role>", Messages: []Message{UserMessage{Content: UserText(text)}}})
			events, err := c.streams().Stream(t.Context(), model, transcript, StreamOptions{APIKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			events.Result()
			mu.Lock()
			defer mu.Unlock()
			if body == "" {
				t.Fatal("no request body reached the server")
			}
			if !strings.Contains(body, text) {
				t.Errorf("request body lacks the literal %q:\n%s", text, body)
			}
			if strings.Contains(body, `\u003c`) || strings.Contains(body, `\u003e`) || strings.Contains(body, `\u0026`) {
				t.Errorf("request body escapes <, > or &:\n%s", body)
			}
		})
	}
}

// A payload the onPayload hook replaced takes a different encoding path in the Anthropic and Google providers than the typed request; both write the
// replacement the way JSON.stringify does.
func TestReplacementPayloadsWriteMarkupLiterally(t *testing.T) {
	const text = "<summary>a & b</summary>"
	check := func(t *testing.T, body []byte) {
		t.Helper()
		if !strings.Contains(string(body), text) || strings.Contains(string(body), `\u003c`) || strings.Contains(string(body), `\u0026`) {
			t.Errorf("replacement body is not written literally: %s", body)
		}
	}
	t.Run("anthropic", func(t *testing.T) {
		body, _, err := anthropicWireBody(map[string]any{"model": "m", "messages": []any{map[string]any{"role": "user", "content": text}}})
		if err != nil {
			t.Fatal(err)
		}
		check(t, body)
	})
	t.Run("anthropic with betas", func(t *testing.T) {
		body, betas, err := anthropicWireBody(map[string]any{"model": "m", "betas": []any{"b1"}, "messages": []any{map[string]any{"role": "user", "content": text}}})
		if err != nil || betas == nil || *betas != "b1" {
			t.Fatalf("betas=%v err=%v", betas, err)
		}
		check(t, body)
	})
	t.Run("google", func(t *testing.T) {
		_, body, err := marshalGeminiParameters(map[string]any{"model": "m", "contents": text}, APIGoogleGenerativeAI)
		if err != nil {
			t.Fatal(err)
		}
		check(t, body)
	})
}

// openrouter-images.ts sends its request with JSON.stringify as well.
func TestOpenRouterImagesRequestWritesMarkupLiterally(t *testing.T) {
	const text = "<summary>a & b</summary>"
	var mu sync.Mutex
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(raw)
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	model := ImageModel{ID: "img", API: APIImagesOpenRouter, Provider: ProviderImagesOpenRouter, BaseURL: server.URL, Output: []string{"image"}}
	GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: []ContentBlock{TextContent{Text: text}}}, ImagesOptions{APIKey: "k"})
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, text) || strings.Contains(body, `\u003c`) || strings.Contains(body, `\u0026`) {
		t.Errorf("images request is not written literally: %s", body)
	}
}
