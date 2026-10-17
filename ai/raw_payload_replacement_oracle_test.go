//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// An extension's before_provider_request handler returns its replacement as JSON text. Pi's onPayload hands the provider the object JSON.parse made of it, and the
// provider sends JSON.stringify of that object (openai-completions and openai-responses through the openai SDK, pi-messages.ts:400, mistral-conversations.ts:312,
// the @google/genai SDK): string escapes as JSON.stringify writes them (\u00e9 is é, a lone surrogate stays escaped), numbers in their shortest form, a repeated key
// in its first place with its last value, and array-index keys first. Pi's streams post to the same server as PiG's; the recorded bodies must be identical.
func TestRawPayloadReplacementsAreSentAsPiSendsThem(t *testing.T) {
	type replacementCase struct {
		Name        string `json:"name"`
		API         string `json:"api"`
		Model       string `json:"model"`
		Replacement string `json:"replacement"`
	}
	const nested = `"dup":1,"n":1.50,"e":1E2,"x":{"b":{"z":1,"z":3},"10":1,"2":2},"dup":2`
	cases := []replacementCase{
		{Name: "openai-completions", API: "openai-completions", Model: "m1",
			Replacement: `{"model":"m1","messages":[{"role":"user","content":"\u00e9 <&> \ud800 \u2028"}],"stream":true,` + nested + `}`},
		{Name: "openai-responses", API: "openai-responses", Model: "m1",
			Replacement: `{"model":"m1","input":[{"role":"user","content":"\u00e9 \ud800"}],"stream":true,` + nested + `}`},
		{Name: "pi-messages", API: "pi-messages", Model: "auto",
			Replacement: `{"model":"auto","context":{"messages":[{"role":"user","content":"\u00e9 \ud800","timestamp":1}]},` + nested + `}`},
		{Name: "mistral-conversations", API: "mistral-conversations", Model: "mistral-small",
			Replacement: `{"model":"mistral-small","max_tokens":1,"messages":[{"role":"user","content":"\u00e9 \ud800"},{"toolCallId":"abc123456","role":"tool","content":[{"imageUrl":"data:x","type":"image_url"}],"name":"n"}],"stream":true,"maxTokens":5.0,` +
				`"responseFormat":{"jsonSchema":{"schemaDefinition":{"z":1,"a":2},"name":"n"},"type":"json_schema"},` + nested + `}`},
		// No replacement: the body Pi built itself.
		{Name: "mistral-conversations-built", API: "mistral-conversations", Model: "mistral-small"},
		{Name: "google-generative-ai", API: "google-generative-ai", Model: "gemini-2.5-flash",
			Replacement: `{"model":"gemini-2.5-flash","contents":[{"role":"user","parts":[{"text":"\u00e9 \ud800"}]}],"config":{"temperature":1.50,"maxOutputTokens":1E2,"systemInstruction":"\u00e9"}}`},
	}

	var mu sync.Mutex
	bodies := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		segments := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
		if len(segments) >= 2 {
			mu.Lock()
			bodies[segments[0]+"/"+segments[1]] = string(body)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	const script = `(async () => {
  const [root, baseUrl, casesJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist', rel)).href);
  const transcript = await load('utils/transcript.js');
  const apis = {
    'openai-completions': (await load('api/openai-completions.lazy.js')).openAICompletionsApi(),
    'openai-responses': (await load('api/openai-responses.lazy.js')).openAIResponsesApi(),
    'pi-messages': (await load('api/pi-messages.lazy.js')).piMessagesApi(),
    'mistral-conversations': (await load('api/mistral-conversations.lazy.js')).mistralConversationsApi(),
    'google-generative-ai': (await load('api/google-generative-ai.lazy.js')).googleGenerativeAIApi(),
  };
  for (const c of JSON.parse(casesJSON)) {
    const model = { id: c.model, name: c.model, provider: 'custom', api: c.api, baseUrl: baseUrl + '/pi/' + c.name, reasoning: false, input: ['text'],
      maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }] });
    await apis[c.api].stream(model, context, { apiKey: 'k', maxRetries: 0, temperature: 0.5, maxTokens: 50, ...(c.replacement ? { onPayload: () => JSON.parse(c.replacement) } : {}) }).result();
  }
})()`
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), server.URL, string(encoded))
	cmd.Stderr = new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			mu.Lock()
			want, ok := bodies["pi/"+c.Name]
			mu.Unlock()
			if !ok {
				t.Fatalf("Pi sent no request for %s", c.Name)
			}
			baseURL := server.URL + "/pig/" + c.Name
			model := &Model{ID: c.Model, Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: "custom", API: API(c.API), BaseURL: baseURL}}
			var provider Provider
			switch c.API {
			case "openai-completions":
				provider = NewOpenAIProvider(OpenAIConfig{Model: c.Model, ModelMetadata: model, ProviderID: "custom", BaseURL: baseURL, APIKey: "k"})
			case "openai-responses":
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{Model: c.Model, ModelMetadata: model, ProviderID: "custom", BaseURL: baseURL, APIKey: "k"})
			case "pi-messages":
				provider = NewPiMessagesProvider(PiMessagesConfig{Model: c.Model, ModelMetadata: model, ProviderID: "custom", BaseURL: baseURL, APIKey: "k"})
			case "mistral-conversations":
				provider = NewMistralProvider(MistralConfig{Model: c.Model, ProviderID: "custom", BaseURL: baseURL, APIKey: "k"})
			case "google-generative-ai":
				provider = NewGoogleProvider(GoogleConfig{Model: c.Model, ModelMetadata: model, ProviderID: "custom", BaseURL: baseURL, APIKey: "k"})
			}
			// The replacements are not valid conversations everywhere; the test-binary request check would stop them before the wire.
			options := StreamOptions{MaxRetries: new(0), Temperature: 0.5, TemperatureSet: true, MaxTokens: 50}
			if c.Replacement != "" {
				options.OnPayload = func(any, *Model) (any, error) { return json.RawMessage(c.Replacement), nil }
			}
			stream, err := provider.Stream(requestvalidity.Allow(t.Context()), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}}), options)
			if err == nil {
				_ = stream.Result()
			}
			mu.Lock()
			got := bodies["pig/"+c.Name]
			mu.Unlock()
			if got != want {
				t.Fatalf("request body\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
