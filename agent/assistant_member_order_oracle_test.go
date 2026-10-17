package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's providers create the members of the finished assistant message in stream order (JSON.stringify writes creation order):
// openai-completions.ts:564 `output.responseId ||= chunk.id` on the first chunk, :566 responseModel when the served model differs, :582
// rawStopReason and :586 errorMessage at the finish reason, then event-stream.ts:127-128 durationMs on the final message. The same stream
// drives Pi's openai-completions stream and PiG's provider; the session-line encoding of PiG's message must equal JSON.stringify of Pi's.
func TestAssistantLateMembersFollowPiCreationOrder(t *testing.T) {
	type oracleStream struct {
		api    string
		events []string
	}
	openAI := func(chunks ...string) oracleStream {
		events := make([]string, 0, len(chunks)+1)
		for _, chunk := range chunks {
			events = append(events, "data: "+chunk)
		}
		return oracleStream{api: "openai-completions", events: append(events, "data: [DONE]")}
	}
	anthropic := func(events ...string) oracleStream { return oracleStream{api: "anthropic-messages", events: events} }
	cases := map[string]oracleStream{
		"content filter": openAI(
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
			`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`,
		),
		"stop": openAI(
			`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
			`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		),
		"id arrives after the model": openAI(
			`{"object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`,
			`{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"served-model","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`,
		),
		// anthropic-messages.ts:588 creates providerThinkingLevel in the output literal after model; :684 and :851 assign cacheWrite1h
		// and reasoning to a usage that already holds totalTokens and cost.
		"anthropic effort and usage splits": anthropic(
			"event: message_start\ndata: "+`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"served-model","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_1h_input_tokens":3}}}}`,
			"event: content_block_start\ndata: "+`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"event: content_block_delta\ndata: "+`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			"event: content_block_stop\ndata: "+`{"type":"content_block_stop","index":0}`,
			"event: message_delta\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4,"output_tokens_details":{"thinking_tokens":2}}}`,
			"event: message_stop\ndata: "+`{"type":"message_stop"}`,
		),
		// message_start always creates cacheWrite1h (`|| 0`, anthropic-messages.ts:684) after cost, even when the stream carries no
		// cache_creation and the delta no thinking tokens; only that assignment places it.
		"anthropic cacheWrite1h from message_start only": anthropic(
			"event: message_start\ndata: "+`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"m1","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1}}}`,
			"event: content_block_start\ndata: "+`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"event: content_block_delta\ndata: "+`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			"event: content_block_stop\ndata: "+`{"type":"content_block_stop","index":0}`,
			"event: message_delta\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			"event: message_stop\ndata: "+`{"type":"message_stop"}`,
		),
		// anthropic-messages.ts:680-684 reads no output_tokens_details from message_start; only message_delta sets reasoning (:849-851).
		"anthropic thinking tokens on message_start only": anthropic(
			"event: message_start\ndata: "+`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"served-model","content":[],"stop_reason":null,"usage":{"input_tokens":5,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens_details":{"thinking_tokens":7}}}}`,
			"event: content_block_start\ndata: "+`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			"event: content_block_delta\ndata: "+`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
			"event: content_block_stop\ndata: "+`{"type":"content_block_stop","index":0}`,
			"event: message_delta\ndata: "+`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			"event: message_stop\ndata: "+`{"type":"message_stop"}`,
		),
	}
	servers := map[string]string{}
	apis := map[string]string{}
	for name, stream := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range stream.events {
				_, _ = fmt.Fprintf(w, "%s\n\n", event)
			}
		}))
		t.Cleanup(server.Close)
		servers[name] = server.URL
		apis[name] = stream.api
	}
	encoded, err := json.Marshal(servers)
	if err != nil {
		t.Fatal(err)
	}
	apiNames, err := json.Marshal(apis)
	if err != nil {
		t.Fatal(err)
	}
	const script = `(async () => {
  const [root, serversJSON, apisJSON] = process.argv.slice(1);
  const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
  const { openAICompletionsApi } = await load("node_modules/@earendil-works/pi-ai/dist/api/openai-completions.lazy.js");
  const { anthropicMessagesApi } = await load("node_modules/@earendil-works/pi-ai/dist/api/anthropic-messages.lazy.js");
  const apis = JSON.parse(apisJSON);
  const { normalizeContext } = await load("node_modules/@earendil-works/pi-ai/dist/utils/transcript.js");
  const out = {};
  for (const [name, url] of Object.entries(JSON.parse(serversJSON))) {
    const api = apis[name];
    const model = { id: "m1", provider: "custom", api, baseUrl: url, reasoning: false, input: ["text"], maxTokens: 1000, contextWindow: 10000,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, ...(api === "anthropic-messages" ? { compat: { supportsMidConvoEffort: true } } : {}) };
    const impl = api === "anthropic-messages" ? anthropicMessagesApi() : openAICompletionsApi();
    const stream = impl.stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" });
    out[name] = JSON.stringify(await stream.result());
  }
  console.log(JSON.stringify(out));
})()`
	root := filepath.Join(testenv.ModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, root, string(encoded), string(apiNames))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var want map[string]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	volatile := regexp.MustCompile(`"(timestamp|durationMs)":\d+`)
	for name, url := range servers {
		t.Run(name, func(t *testing.T) {
			provider := ai.NewOpenAIProvider(ai.OpenAIConfig{Model: "m1", ProviderID: "custom", BaseURL: url, APIKey: "k"})
			if apis[name] == "anthropic-messages" {
				provider = ai.NewAnthropicProvider(ai.AnthropicConfig{Model: "m1", ProviderID: "custom", BaseURL: url, APIKey: "k", Compat: &ai.ModelCompat{SupportsMidConvoEffort: new(true)}})
			}
			stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}}), ai.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for range stream.Events(t.Context()) {
			}
			result := stream.Result()
			got, err := json.Marshal(AgentMessage{Assistant: agentAssistantMessage(result)})
			if err != nil {
				t.Fatal(err)
			}
			if g, w := volatile.ReplaceAllString(string(got), `"$1":0`), volatile.ReplaceAllString(want[name], `"$1":0`); g != w {
				t.Fatalf("assistant message\n got: %s\nwant: %s", g, w)
			}
		})
	}
}
