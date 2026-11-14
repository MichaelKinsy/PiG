package coding

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// firstRequestURL is the URL of the first request model sends. The transport of the test answers it, so nothing leaves the process.
func firstRequestURL(t *testing.T, model *ai.Model) string {
	t.Helper()
	var target string
	fetch := &http.Client{Transport: cloudflareCompatTransport(func(request *http.Request) (*http.Response, error) {
		if target == "" {
			target = request.URL.String()
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"fixture"}}`))}, nil
	})}
	stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{}}), ai.StreamOptions{Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	return target
}

// Pi gives no provider id a base URL of its own. A model is built with the base URL its definition, its provider or its catalog entry states
// (provider-composer.ts:224-225), and the OpenAI completions request hands that value to the OpenAI client unchanged
// (packages/ai/src/api/openai-completions.ts:778-780), whose only default is https://api.openai.com/v1 (openai client.js:202). OLLAMA_HOST is
// read nowhere in Pi. So a model of the provider id ollama without a base URL is addressed like a model of any other OpenAI-compatible
// provider id without one, and never to OLLAMA_HOST or http://localhost:11434/v1.
func TestBuildModelGivesTheProviderIDOllamaNoBaseURLOfItsOwn(t *testing.T) {
	const openAIDefault = "https://api.openai.com/v1/chat/completions"
	for _, tc := range []struct {
		name, provider, baseURL, ollamaHost, want string
	}{
		{name: "ollama without a base URL", provider: "ollama", want: openAIDefault},
		{name: "ollama without a base URL, OLLAMA_HOST set", provider: "ollama", ollamaHost: "http://127.0.0.1:9/host/v1", want: openAIDefault},
		{name: "ollama with a base URL, OLLAMA_HOST set", provider: "ollama", baseURL: "http://127.0.0.1:9/own/v1", ollamaHost: "http://127.0.0.1:9/host/v1", want: "http://127.0.0.1:9/own/v1/chat/completions"},
		{name: "another OpenAI-compatible provider without a base URL, OLLAMA_HOST set", provider: "mylocal", ollamaHost: "http://127.0.0.1:9/host/v1", want: openAIDefault},
		{name: "another OpenAI-compatible provider with a base URL", provider: "mylocal", baseURL: "http://127.0.0.1:9/own/v1", want: "http://127.0.0.1:9/own/v1/chat/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnvKeys(t)
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("PIG_HOME", t.TempDir())
			t.Setenv("OLLAMA_HOST", tc.ollamaHost)
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			entry := icodingagent.ModelEntry{API: string(ai.APIOpenAICompletions), BaseURL: tc.baseURL, APIKey: "fixture-key"}
			fromEntry, err := BuildModelFromEntry(tc.provider, "m1", entry, services)
			if err != nil {
				t.Fatal(err)
			}
			if got := firstRequestURL(t, fromEntry); got != tc.want {
				t.Errorf("BuildModelFromEntry: the request went to %q, want %q", got, tc.want)
			}
			if tc.baseURL != "" {
				return
			}
			// The spec form builds only a model some list names; no models.json names this provider's model, so the spec form has no model (and no host to send a key to).
			if fromSpec, err := BuildModel(tc.provider+"/m1", services); fromSpec != nil || err == nil || err.Error() != "Model not found: "+tc.provider+"/m1" {
				t.Errorf("BuildModel = %v, %v; want no model and Pi's 'Model not found'", fromSpec, err)
			}
		})
	}
}
