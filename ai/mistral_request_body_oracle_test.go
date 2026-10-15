//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Pi sends JSON.stringify(toMistralWirePayload(payload)) (mistral-conversations.ts:312): the payload's members in the order buildChatPayload creates them
// (model, stream, messages, tools, temperature, maxTokens, toolChoice, ..., :519-532), messages and chunks in toChatMessages' order (:794-890), and each SDK
// property renamed by remapMistralProperty, which puts a new snake_case member last (:433-437). Pi's stream and PiG's post to the same server; the bodies must
// be identical.
func TestMistralRequestBodyMatchesPi(t *testing.T) {
	type bodyCase struct {
		Name    string          `json:"name"`
		Images  bool            `json:"images"`
		History json.RawMessage `json:"history"`
		Options json.RawMessage `json:"options"`
	}
	const usage = `"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	cases := []bodyCase{
		{Name: "history with tools, images and options", Images: true,
			History: json.RawMessage(`[{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"ok"},{"type":"toolCall","id":"abc123456","name":"lookup","arguments":{"z":"q","a":1}}],"api":"mistral-conversations","provider":"mistral","model":"mistral-small",` + usage + `,"stopReason":"toolUse","timestamp":2},` +
				`{"role":"toolResult","toolCallId":"abc123456","toolName":"lookup","content":[{"type":"text","text":"r"},{"type":"image","data":"aGk=","mimeType":"image/png"}],"isError":true,"timestamp":3},{"role":"user","content":"next","timestamp":4}]`),
			Options: json.RawMessage(`{"temperature":0.5,"maxTokens":100,"toolChoice":"any","sessionId":"s1"}`)},
		{Name: "no options", History: json.RawMessage(`[]`), Options: json.RawMessage(`{}`)},
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
  const api = (await load('api/mistral-conversations.lazy.js')).mistralConversationsApi();
  for (const [index, c] of JSON.parse(casesJSON).entries()) {
    const model = { id: 'mistral-small', name: 'mistral-small', provider: 'mistral', api: 'mistral-conversations', baseUrl: baseUrl + '/pi/' + index, reasoning: false,
      input: c.images ? ['text', 'image'] : ['text'], maxTokens: 1000, contextWindow: 10000, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
    const tools = [{ name: 'lookup', description: 'Look up', parameters: { type: 'object', properties: { z: { type: 'string' }, a: { type: 'number' } }, required: ['z'] } }];
    const context = transcript.normalizeContext({ systemPrompt: 'be brief', messages: [{ role: 'user', content: 'hi', timestamp: 1 }, ...c.history], tools });
    await api.stream(model, context, { apiKey: 'k', maxRetries: 0, ...c.options }).result();
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

	for index, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			mu.Lock()
			want, ok := bodies["pi/"+string(rune('0'+index))]
			mu.Unlock()
			if !ok {
				t.Fatal("Pi sent no request")
			}
			messages := []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}
			var history []json.RawMessage
			if err := json.Unmarshal(c.History, &history); err != nil {
				t.Fatal(err)
			}
			for _, raw := range history {
				var role struct {
					Role string `json:"role"`
				}
				if err := json.Unmarshal(raw, &role); err != nil {
					t.Fatal(err)
				}
				var message Message
				var err error
				switch role.Role {
				case "assistant":
					var assistant AssistantMessage
					err = json.Unmarshal(raw, &assistant)
					message = assistant
				case "toolResult":
					var result ToolResultMessage
					err = json.Unmarshal(raw, &result)
					message = result
				default:
					var user struct {
						Content   string `json:"content"`
						Timestamp int64  `json:"timestamp"`
					}
					err = json.Unmarshal(raw, &user)
					message = UserMessage{Content: UserText(user.Content), Timestamp: user.Timestamp}
				}
				if err != nil {
					t.Fatal(err)
				}
				messages = append(messages, message)
			}
			var piOptions struct {
				Temperature *float64 `json:"temperature"`
				MaxTokens   int      `json:"maxTokens"`
				ToolChoice  any      `json:"toolChoice"`
				SessionID   string   `json:"sessionId"`
			}
			if err := json.Unmarshal(c.Options, &piOptions); err != nil {
				t.Fatal(err)
			}
			options := StreamOptions{MaxRetries: new(0), MaxTokens: piOptions.MaxTokens, ToolChoice: piOptions.ToolChoice, SessionID: piOptions.SessionID}
			if piOptions.Temperature != nil {
				options.Temperature, options.TemperatureSet = *piOptions.Temperature, true
			}
			input := []string{"text"}
			if c.Images {
				input = append(input, "image")
			}
			baseURL := server.URL + "/pig/" + string(rune('0'+index))
			provider := NewMistralProvider(MistralConfig{Model: "mistral-small", ProviderID: "mistral", BaseURL: baseURL, APIKey: "k",
				ModelMetadata: &Model{ID: "mistral-small", Input: input, ProviderMeta: ProviderMetadata{ProviderID: "mistral", API: APIMistralConversations, BaseURL: baseURL}}})
			tools := mustTools(t, `[{"name":"lookup","description":"Look up","parameters":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}},"required":["z"]}}]`)
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "be brief", Messages: messages, Tools: tools}), options)
			if err == nil {
				_ = stream.Result()
			}
			mu.Lock()
			got := bodies["pig/"+string(rune('0'+index))]
			mu.Unlock()
			if got != want {
				t.Fatalf("request body\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// The typed lowering of a mistralRequest writes the bytes the general one (Pi's toMistralWirePayload on the payload's JSON) writes, for every member set.
func TestMistralTypedWirePayloadMatchesTheGeneralLowering(t *testing.T) {
	text, temperature, maxTokens, prefix := "t", 0.5, 9, false
	request := mistralRequest{Model: "m", Stream: true, Messages: []mistralMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: []mistralContentChunk{{Type: "text", Text: &text}, {Type: "image_url", ImageURL: "data:x"}}},
		{Role: "assistant", Prefix: &prefix, Content: []mistralContentChunk{{Type: "thinking", Thinking: []mistralTextChunk{{Type: "text", Text: "hm"}}}},
			ToolCalls: []mistralToolCallMsg{{ID: "abc123456", Type: "function", Function: mistralToolCallFnMsg{Name: "n", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "abc123456", Name: "n", Content: []mistralContentChunk{{Type: "text", Text: &text}}},
	}, Tools: []mistralTool{{Type: "function", Function: mistralToolFn{Name: "n", Description: "d", Parameters: map[string]any{"type": "object"}}}},
		Temperature: &temperature, MaxTokens: &maxTokens, ToolChoice: "any", PromptMode: "reasoning", ReasoningEffort: "high", PromptCacheKey: "s1"}
	typed, err := toMistralWirePayload(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := marshalRequestBody(request)
	if err != nil {
		t.Fatal(err)
	}
	general, err := toMistralWirePayload(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(typed) != string(general) {
		t.Fatalf("typed lowering\n got: %s\nwant: %s", typed, general)
	}
	// A member added to mistralRequest must be set here, so that a wire() that drops it fails the comparison.
	fields := reflect.ValueOf(request)
	for i := range fields.NumField() {
		if fields.Field(i).IsZero() {
			t.Fatalf("mistralRequest.%s is not set", fields.Type().Field(i).Name)
		}
	}
}
