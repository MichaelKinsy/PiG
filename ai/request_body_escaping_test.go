package ai_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's provider SDKs send JSON.stringify(body) (openai 7.19.0 internal/request-options.js:29; the Anthropic, Google GenAI and Mistral SDKs
// stringify the same way), and JSON.stringify writes '<', '>', '&', U+2028 and U+2029 as they are. encoding/json escapes all five.
func TestProviderRequestBodiesWriteCharactersAsJSONStringifyDoes(t *testing.T) {
	// The long text takes openai-completions' spliced encoding of large message strings.
	for _, text := range []string{"a <b> & c \u2028 d \u2029 e", strings.Repeat("a <b> & c \u2028 d \u2029 e ", 4096)} {
		for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses, ai.APIAnthropicMessages, ai.APIGoogleGenerativeAI, ai.APIMistralConversations, ai.APIAzureOpenAIResponses, ai.APIOpenAICodexResponses} {
			t.Run(string(api), func(t *testing.T) {
				var body []byte
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ = io.ReadAll(r.Body)
					if r.Header.Get("Content-Encoding") == "zstd" {
						// The Codex SSE body is compressed as Pi's compressRequestBodyZstd does; the escaping is a property of the JSON inside.
						decoder, err := zstd.NewReader(nil)
						if err != nil {
							t.Error(err)
							return
						}
						defer decoder.Close()
						if body, err = decoder.DecodeAll(body, nil); err != nil {
							t.Error(err)
							return
						}
					}
					w.WriteHeader(http.StatusBadRequest)
				}))
				defer server.Close()
				provider := matrixProvider(t, &ai.GeneratedModel{ID: "m1", Provider: "custom", API: api}, server.URL)
				stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}}), ai.StreamOptions{})
				// The server answers 400; a provider that sends its request before returning reports that as Stream's error.
				if err == nil {
					for range stream.Events(t.Context()) {
					}
				}
				if len(body) == 0 {
					t.Fatalf("no request body (Stream error %v)", err)
				}
				if !bytes.Contains(body, []byte(text)) {
					t.Fatalf("request body does not hold the text as JSON.stringify writes it:\n%.400s", body)
				}
			})
		}
	}
}
