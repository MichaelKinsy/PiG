//go:build parity

package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// configureProviderFixture runs a real HTTP provider endpoint and gives each binary the same models.json contract in its isolated agent directory.
func configureProviderFixture(t *testing.T, sc *Scenario, env []string, cwd, temp string) []string {
	t.Helper()
	api, err := providerFixtureAPI(sc)
	if err != nil {
		t.Fatal(err)
	}
	if api == "" {
		return env
	}
	fixture := providerFixtures[api]
	server := httptest.NewServer(fixture.handler())
	t.Cleanup(server.Close)
	agent := resultIdentityRoots(cwd, temp, env)["agent"]
	if agent == "" {
		t.Fatal("provider fixture requires an isolated agent directory")
	}
	if err := os.MkdirAll(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	models := map[string]any{"providers": map[string]any{"strict-wire": map[string]any{"api": api, "baseUrl": server.URL + fixture.baseURLPath, "apiKey": "fixture-key", "models": []any{map[string]any{"id": "strict", "name": "strict", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 1000}}}}}
	data, err := json.Marshal(models)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return env
}

// readArguments is the tool-call argument object every fixture returns for a READ prompt.
const readArguments = `{"path":"parity-read-target.txt"}`

// providerFixture describes one hermetic provider API: where its base URL points, how to read the newest turn from a request, and how to write the two replies (a read tool call and a text echo).
type providerFixture struct {
	baseURLPath string
	// lastTurn returns the newest conversation turn's text and whether that turn is a tool result.
	lastTurn func(body []byte) (text string, toolResult bool, err error)
	toolCall func() []sseEvent
	reply    func(text string) []sseEvent
	// errorBody is the JSON returned with HTTP 400 for the HTTP_ERROR prompt.
	errorBody string
}

// sseEvent is one server-sent event; an empty name writes a data-only record.
type sseEvent struct {
	name string
	data any
}

var providerFixtures = map[string]providerFixture{
	"openai-completions":    {baseURLPath: "/v1", lastTurn: chatLastTurn, toolCall: openAIToolCall("strict-read"), reply: openAIReply, errorBody: chatErrorBody},
	"mistral-conversations": {baseURLPath: "", lastTurn: chatLastTurn, toolCall: openAIToolCall("readcall1"), reply: openAIReply, errorBody: chatErrorBody},
	"anthropic-messages":    {baseURLPath: "", lastTurn: anthropicLastTurn, toolCall: anthropicToolCall, reply: anthropicReply, errorBody: `{"type":"error","error":{"type":"invalid_request_error","message":"strict wire bad request"}}`},
	"google-generative-ai":  {baseURLPath: "/v1beta", lastTurn: googleLastTurn, toolCall: googleToolCall, reply: googleReply, errorBody: `{"error":{"code":400,"message":"strict wire bad request","status":"INVALID_ARGUMENT"}}`},
}

const chatErrorBody = `{"error":{"message":"strict wire bad request","type":"invalid_request_error"}}`

// providerFixtureAPI resolves the scenario's fixture API; the empty string means no fixture.
func providerFixtureAPI(sc *Scenario) (string, error) {
	api := sc.ProviderFixture
	if sc.OpenAIFixture {
		if api != "" {
			return "", fmt.Errorf("scenario %s sets both openai_fixture and provider_fixture", sc.Name)
		}
		return "openai-completions", nil
	}
	if _, ok := providerFixtures[api]; api != "" && !ok {
		return "", fmt.Errorf("scenario %s: unknown provider_fixture %q", sc.Name, api)
	}
	return api, nil
}

// handler serves one provider turn per request: HTTP_ERROR fails, READ from a user turn calls the read tool, and any other newest turn is echoed as text.
func (f providerFixture) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		text, toolResult, err := f.lastTurn(body)
		if err != nil {
			http.Error(w, "invalid fixture request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if text == "HTTP_ERROR" {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, f.errorBody)
			return
		}
		events := f.reply(text)
		if !toolResult && text == "READ" {
			events = f.toolCall()
		}
		payload, err := encodeSSE(events)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// One Content-Length write delivers headers and the whole body together, as the D82 probe server does.
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("content-length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	})
}

func encodeSSE(events []sseEvent) ([]byte, error) {
	var b bytes.Buffer
	for _, event := range events {
		if event.name != "" {
			fmt.Fprintf(&b, "event: %s\n", event.name)
		}
		if text, ok := event.data.(string); ok {
			fmt.Fprintf(&b, "data: %s\n\n", text)
			continue
		}
		data, err := json.Marshal(event.data)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "data: %s\n\n", data)
	}
	return b.Bytes(), nil
}

// contentText joins the text of a message content that is a string or an array of blocks.
func contentText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, block := range blocks {
		b.WriteString(block.Text)
	}
	return b.String(), nil
}

// chatLastTurn reads OpenAI-shaped chat messages: the newest message's role and content.
func chatLastTurn(body []byte) (string, bool, error) {
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Messages) == 0 {
		return "", false, fmt.Errorf("no messages")
	}
	last := request.Messages[len(request.Messages)-1]
	text, err := contentText(last.Content)
	return text, last.Role == "tool", err
}

func openAIChunk(delta map[string]any, finish string) sseEvent {
	chunk := map[string]any{"id": "chatcmpl-strict", "model": "strict", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13}}
	return sseEvent{data: chunk}
}

func openAIToolCall(callID string) func() []sseEvent {
	return func() []sseEvent {
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": callID, "type": "function", "function": map[string]any{"name": "read", "arguments": readArguments}}}}
		return []sseEvent{openAIChunk(delta, "tool_calls"), {data: "[DONE]"}}
	}
}

func openAIReply(text string) []sseEvent {
	return []sseEvent{openAIChunk(map[string]any{"content": text}, "stop"), {data: "[DONE]"}}
}

// anthropicLastTurn reads Anthropic messages: user text blocks or the tool_result content of the newest message.
func anthropicLastTurn(body []byte) (string, bool, error) {
	var request struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Messages) == 0 {
		return "", false, fmt.Errorf("no messages")
	}
	raw := request.Messages[len(request.Messages)-1].Content
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, false, nil
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false, err
	}
	var b strings.Builder
	toolResult := false
	for _, block := range blocks {
		if block.Type == "tool_result" {
			toolResult = true
			result, err := contentText(block.Content)
			if err != nil {
				return "", false, err
			}
			b.WriteString(result)
			continue
		}
		b.WriteString(block.Text)
	}
	return b.String(), toolResult, nil
}

func anthropicStream(stopReason string, blocks ...sseEvent) []sseEvent {
	events := []sseEvent{{name: "message_start", data: map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_strict", "type": "message", "role": "assistant", "model": "strict", "content": []any{}, "stop_reason": nil, "usage": map[string]int{"input_tokens": 10, "output_tokens": 1}}}}}
	events = append(events, blocks...)
	return append(events,
		sseEvent{name: "message_delta", data: map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason}, "usage": map[string]int{"output_tokens": 3}}},
		sseEvent{name: "message_stop", data: map[string]any{"type": "message_stop"}})
}

func anthropicBlock(start map[string]any, delta map[string]any) []sseEvent {
	return []sseEvent{
		{name: "content_block_start", data: map[string]any{"type": "content_block_start", "index": 0, "content_block": start}},
		{name: "content_block_delta", data: map[string]any{"type": "content_block_delta", "index": 0, "delta": delta}},
		{name: "content_block_stop", data: map[string]any{"type": "content_block_stop", "index": 0}},
	}
}

func anthropicToolCall() []sseEvent {
	return anthropicStream("tool_use", anthropicBlock(
		map[string]any{"type": "tool_use", "id": "toolu_strict_read", "name": "read", "input": map[string]any{}},
		map[string]any{"type": "input_json_delta", "partial_json": readArguments})...)
}

func anthropicReply(text string) []sseEvent {
	return anthropicStream("end_turn", anthropicBlock(
		map[string]any{"type": "text", "text": ""},
		map[string]any{"type": "text_delta", "text": text})...)
}

// googleLastTurn reads Gemini contents: text parts or the functionResponse output of the newest content.
func googleLastTurn(body []byte) (string, bool, error) {
	var request struct {
		Contents []struct {
			Parts []struct {
				Text             string `json:"text"`
				FunctionResponse *struct {
					Response struct {
						Output string `json:"output"`
						Error  string `json:"error"`
					} `json:"response"`
				} `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Contents) == 0 {
		return "", false, fmt.Errorf("no contents")
	}
	var b strings.Builder
	toolResult := false
	for _, part := range request.Contents[len(request.Contents)-1].Parts {
		if part.FunctionResponse != nil {
			toolResult = true
			b.WriteString(part.FunctionResponse.Response.Output + part.FunctionResponse.Response.Error)
			continue
		}
		b.WriteString(part.Text)
	}
	return b.String(), toolResult, nil
}

func googleChunk(part map[string]any) []sseEvent {
	return []sseEvent{{data: map[string]any{
		"candidates":    []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{part}}, "finishReason": "STOP"}},
		"usageMetadata": map[string]int{"promptTokenCount": 10, "candidatesTokenCount": 3, "totalTokenCount": 13},
		"modelVersion":  "strict",
		"responseId":    "gemini-strict",
	}}}
}

func googleToolCall() []sseEvent {
	return googleChunk(map[string]any{"functionCall": map[string]any{"name": "read", "args": map[string]any{"path": "parity-read-target.txt"}}})
}

func googleReply(text string) []sseEvent { return googleChunk(map[string]any{"text": text}) }
