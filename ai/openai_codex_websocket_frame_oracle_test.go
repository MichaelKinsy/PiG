package ai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

// Pi sends each WebSocket request as JSON.stringify({ type: "response.create", ...requestBody }) (openai-codex-responses.ts:1542): type
// first, then the request body's members in buildRequestBody order. A cached continuation is { ...body, previous_response_id, input }
// (:1473-1476): previous_response_id is appended and input keeps its place. Pi and PiG run the same two turns against one WebSocket
// server, which records the frames each sends.
func TestCodexWebSocketFramesMatchPi(t *testing.T) {
	var mu sync.Mutex
	var frames []string
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			frames = append(frames, string(data))
			index := strconv.Itoa(len(frames))
			mu.Unlock()
			for _, event := range []string{
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_` + index + `","role":"assistant","status":"in_progress","content":[]}}`,
				`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"answer"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_` + index + `","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}}`,
				`{"type":"response.completed","response":{"id":"resp_` + index + `","status":"completed","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
			} {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()
	token := codexTestToken(t, "acct_ws")
	schema := `{"type":"object","properties":{"z":{"type":"string","description":"zed"},"a":{"type":"number"}},"required":["z"]}`

	const script = `(async () => {
  const [root, baseUrl, token, schema] = process.argv.slice(1);
  const load = (rel) => import(require('node:url').pathToFileURL(require('node:path').join(root, rel)).href);
  const lazy = await load('node_modules/@earendil-works/pi-ai/dist/api/openai-codex-responses.lazy.js');
  const transcript = await load('node_modules/@earendil-works/pi-ai/dist/utils/transcript.js');
  const model = { id: 'gpt-5.2', name: 'GPT', provider: 'openai-codex', api: 'openai-codex-responses', baseUrl, reasoning: false, input: ['text'],
    maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
  const tools = [{ name: 'lookup', description: 'Look up', parameters: JSON.parse(schema) }];
  const options = { apiKey: token, transport: 'auto', sessionId: 'session-ws' };
  const one = { role: 'user', content: 'one', timestamp: 1 };
  const first = await lazy.openAICodexResponsesApi().stream(model, transcript.normalizeContext({ systemPrompt: 'be brief', messages: [one], tools }), options).result();
  if (first.stopReason !== 'stop') throw new Error('first: ' + first.errorMessage);
  const messages = [one, first, { role: 'user', content: 'two', timestamp: 2 }];
  const second = await lazy.openAICodexResponsesApi().stream(model, transcript.normalizeContext({ systemPrompt: 'be brief', messages, tools }), options).result();
  if (second.stopReason !== 'stop') throw new Error('second: ' + second.errorMessage);
  const third = await lazy.openAICodexResponsesApi().stream(model, transcript.normalizeContext({ systemPrompt: 'be terse', messages: [...messages, second, { role: 'user', content: 'three', timestamp: 3 }], tools }), options).result();
  if (third.stopReason !== 'stop') throw new Error('third: ' + third.errorMessage);
  process.exit(0);
})()`
	cmd := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), server.URL, token, schema)
	cmd.Stderr = new(bytes.Buffer)
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, cmd.Stderr)
	}
	mu.Lock()
	want := frames
	frames = nil
	mu.Unlock()
	if len(want) != 3 {
		t.Fatalf("Pi sent %d frames, want 3", len(want))
	}

	defer CloseOpenAICodexWebSocketSessions()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: token, Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL})
	tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":`+schema+`}]`)
	options := StreamOptions{Transport: TransportAuto, SessionID: "session-ws"}
	one := UserMessage{Content: UserText("one"), Timestamp: 1}
	firstStream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{one}, Tools: tools}), options)
	if err != nil {
		t.Fatal(err)
	}
	first := firstStream.Result()
	if first.StopReason != StopReasonStop {
		t.Fatalf("first: %s", first.ErrorMessage)
	}
	secondMessages := []Message{one, *first, UserMessage{Content: UserText("two"), Timestamp: 2}}
	secondContext := NormalizeContext(Context{SystemPrompt: "be brief", Messages: secondMessages, Tools: tools})
	secondStream, err := provider.Stream(t.Context(), secondContext, options)
	if err != nil {
		t.Fatal(err)
	}
	second := secondStream.Result()
	if second.StopReason != StopReasonStop {
		t.Fatalf("second: %s", second.ErrorMessage)
	}
	// The third turn changes instructions, a member other than input, so the bodies differ by JSON text and the request carries the
	// full context (requestBodiesMatchExceptInput, openai-codex-responses.ts:1434-1444).
	thirdContext := NormalizeContext(Context{SystemPrompt: "be terse", Messages: append(slices.Clone(secondMessages), *second, UserMessage{Content: UserText("three"), Timestamp: 3}), Tools: tools})
	thirdStream, err := provider.Stream(t.Context(), thirdContext, options)
	if err != nil {
		t.Fatal(err)
	}
	if third := thirdStream.Result(); third.StopReason != StopReasonStop {
		t.Fatalf("third: %s", third.ErrorMessage)
	}
	mu.Lock()
	got := frames
	mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("PiG sent %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("frame %d\n got: %s\nwant: %s", i+1, got[i], want[i])
		}
	}
	var continuation struct {
		PreviousResponseID string            `json:"previous_response_id"`
		Input              []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal([]byte(want[1]), &continuation); err != nil || continuation.PreviousResponseID != "resp_1" || len(continuation.Input) != 1 {
		t.Fatalf("Pi's second frame is not a cached continuation: %s", want[1])
	}
	var full struct {
		PreviousResponseID *string `json:"previous_response_id"`
	}
	if err := json.Unmarshal([]byte(want[2]), &full); err != nil || full.PreviousResponseID != nil {
		t.Fatalf("Pi's third frame is not a full-context request: %s", want[2])
	}
}
