package ai

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Oracle for packages/ai/src/api/openai-responses.lazy.ts openAIResponsesApi and lazy.ts lazyApi: Pi's own lazy API returns a
// stream at once and builds the provider behind it, so a setup failure (openai-responses.ts:58-61 getClientApiKey throws "No API
// key for provider: <provider>" when the options carry no apiKey) ends that stream with the error message lazy.ts
// createSetupErrorMessage builds: no content, zero usage, stopReason "error", the model's api, provider and id, and the error text.
func TestOpenAIResponsesAPISetupFailureEndsTheStreamLikePi(t *testing.T) {
	const script = `(async () => {
  const [root, modelJSON] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/openai-responses.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const model = JSON.parse(modelJSON);
  const context = transcript.normalizeContext({ messages: [{ role: 'user', content: 'hi', timestamp: 1 }] });
  const events = [];
  const stream = lazy.openAIResponsesApi().stream(model, context, {});
  for await (const event of stream) events.push(event.type);
  const message = await stream.result();
  console.log(JSON.stringify({ events, stopReason: message.stopReason, errorMessage: message.errorMessage, api: message.api, provider: message.provider, model: message.model, content: message.content, usage: message.usage }));
})()`
	model := sharedModel("gpt-5", "openai", APIOpenAIResponses, true, []string{"text"}, nil)
	piModel := modelWire(model)
	piModel["baseUrl"] = "https://api.openai.com/v1"
	wire, err := json.Marshal(piModel)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(wire)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var want struct {
		Events       []string `json:"events"`
		StopReason   string   `json:"stopReason"`
		ErrorMessage string   `json:"errorMessage"`
		API          string   `json:"api"`
		Provider     string   `json:"provider"`
		Model        string   `json:"model"`
		Content      []any    `json:"content"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	t.Setenv("OPENAI_API_KEY", "")
	stream, err := OpenAIResponsesAPI().Stream(t.Context(), model, newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}), StreamOptions{})
	if err != nil {
		t.Fatalf("Stream returned %v; the lazy API returns the stream at once", err)
	}
	var events []string
	for event := range stream.Events(t.Context()) {
		events = append(events, string(event.EventType()))
	}
	got := stream.Result()
	if got.StopReason != StopReason(want.StopReason) || got.ErrorMessage != want.ErrorMessage || string(got.API) != want.API || got.Provider != want.Provider || got.Model != want.Model || len(got.Content) != len(want.Content) {
		t.Fatalf("setup failure result = {stop %q error %q api %q provider %q model %q content %d}, Pi = %+v", got.StopReason, got.ErrorMessage, got.API, got.Provider, got.Model, len(got.Content), want)
	}
	if strings.Join(events, ",") != strings.Join(want.Events, ",") {
		t.Fatalf("events = %v, Pi = %v", events, want.Events)
	}
}
