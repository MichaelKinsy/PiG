package ai

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Every API module's stream asserts request authentication inside its own try block (anthropic-messages.ts:611-613,
// azure-openai-responses.ts:68-69, google-generative-ai.ts:91-92, mistral-conversations.ts:137-138,
// openai-codex-responses.ts:266-267, openai-completions.ts:341, openai-responses.ts:157, pi-messages.ts:366-367), so a
// missing key ends the stream with `options?.signal?.aborted ? "aborted" : "error"`. Pi's own lazy APIs and the Go APIs
// stream the same request with an aborted and a live signal, and must end alike.
func TestAPIStreamMissingAuthStopReasonMatchesPi(t *testing.T) {
	apis := []struct {
		api     API
		piName  string
		streams *ProviderStreams
	}{
		{APIAnthropicMessages, "anthropicMessagesApi", AnthropicMessagesAPI()},
		{APIAzureOpenAIResponses, "azureOpenAIResponsesApi", AzureOpenAIResponsesAPI()},
		{APIGoogleGenerativeAI, "googleGenerativeAIApi", GoogleGenerativeAIAPI()},
		{APIMistralConversations, "mistralConversationsApi", MistralConversationsAPI()},
		{APIOpenAICodexResponses, "openAICodexResponsesApi", OpenAICodexResponsesAPI()},
		{APIOpenAICompletions, "openAICompletionsApi", OpenAICompletionsAPI()},
		{APIOpenAIResponses, "openAIResponsesApi", OpenAIResponsesAPI()},
		{APIPiMessages, "piMessagesApi", PiMessagesAPI()},
	}
	const script = `(async () => {
  const [root, listJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, 'node_modules/@earendil-works/pi-ai/dist', rel)).href);
  const transcript = await load('utils/transcript.js');
  const out = [];
  for (const [api, name] of JSON.parse(listJSON)) {
    const lazy = await load('api/' + api + '.lazy.js');
    const model = { id: 'm', name: 'm', api, provider: 'p', baseUrl: 'http://127.0.0.1:1', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
    for (const aborted of [true, false]) {
      const controller = new AbortController();
      if (aborted) controller.abort();
      const stream = lazy[name]().stream(model, transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }] }), { signal: controller.signal });
      const events = [];
      for await (const event of stream) events.push(event.type);
      const message = await stream.result();
      out.push({ events, stopReason: message.stopReason, errorMessage: message.errorMessage });
    }
  }
  console.log(JSON.stringify(out));
})()`
	list := make([][2]string, len(apis))
	for i, a := range apis {
		list[i] = [2]string{string(a.api), a.piName}
	}
	listJSON, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(listJSON))
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "AZURE_OPENAI_API_KEY", "GEMINI_API_KEY", "MISTRAL_API_KEY", "OPENAI_API_KEY"} {
		t.Setenv(name, "")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var want []struct {
		Events       []string `json:"events"`
		StopReason   string   `json:"stopReason"`
		ErrorMessage string   `json:"errorMessage"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}
	if len(want) != 2*len(apis) {
		t.Fatalf("oracle returned %d results, want %d", len(want), 2*len(apis))
	}
	for i, a := range apis {
		for j, aborted := range []bool{true, false} {
			w := want[2*i+j]
			model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: a.api, ProviderID: "p", BaseURL: "http://127.0.0.1:1"}, Capabilities: ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}
			ctx, cancel := context.WithCancel(t.Context())
			if aborted {
				cancel()
			}
			stream, err := a.streams.Stream(ctx, model, newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}), StreamOptions{})
			if err != nil {
				cancel()
				t.Fatalf("%s: Stream returned %v; the lazy API returns the stream at once", a.api, err)
			}
			var events []string
			for event := range stream.Events(t.Context()) {
				events = append(events, string(event.EventType()))
			}
			got := stream.Result()
			cancel()
			if string(got.StopReason) != w.StopReason || got.ErrorMessage != w.ErrorMessage || strings.Join(events, ",") != strings.Join(w.Events, ",") {
				t.Errorf("%s aborted=%v: stop %q error %q events %v; Pi stop %q error %q events %v", a.api, aborted, got.StopReason, got.ErrorMessage, events, w.StopReason, w.ErrorMessage, w.Events)
			}
		}
	}
}
