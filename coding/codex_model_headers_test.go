package coding

// pi: packages/coding-agent/src/core/provider-attribution.ts

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream: packages/ai/test/openai-codex-stream.test.ts:800 (#10429): a models.json header named originator or
// User-Agent replaces the Codex default; Authorization and chatgpt-account-id stay the provider's.
func TestCodexModelsJSONHeadersOverrideIdentityDefaults(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n"))
	}))
	t.Cleanup(server.Close)
	token := "header." + base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_real"}}`)) + ".signature"
	provider, err := buildProviderForEntry("openai-codex", "gpt-5.1-codex", ai.APIOpenAICodexResponses, icodingagent.ModelEntry{
		ProviderID: "openai-codex", ModelID: "gpt-5.1-codex", BaseURL: server.URL, APIKey: token, API: string(ai.APIOpenAICodexResponses),
		Reasoning: true, Input: []string{"text"},
		Headers: map[string]string{"originator": "my-app", "user-agent": "my-app/1.0", "Authorization": "Bearer ignored"},
	}, newRuntimeTestServices(t), "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{Transport: ai.TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	for name, want := range map[string]string{"originator": "my-app", "User-Agent": "my-app/1.0", "Authorization": "Bearer " + token, "chatgpt-account-id": "acct_real"} {
		if v := got.Values(name); !reflect.DeepEqual(v, []string{want}) {
			t.Errorf("%s = %q, want [%s]", name, v, want)
		}
	}
}
