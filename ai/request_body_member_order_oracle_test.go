package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// wireCapture records, per URL path prefix, the raw requests a test server received: HTTP bodies with their anthropic-beta header, and WebSocket text frames in order.
type wireCapture struct {
	mu       sync.Mutex
	requests map[string][]string
}

func (c *wireCapture) add(prefix, record string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests[prefix] = append(c.requests[prefix], record)
}

func (c *wireCapture) get(prefix string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.requests[prefix]...)
}

// requestBodyOrderServer answers Anthropic Messages POSTs with one streamed text reply and Codex WebSocket requests with one completed message per request frame.
// The first two path segments ("/pi/3", "/pig/codex") name the sender and the probe.
func requestBodyOrderServer(t *testing.T, capture *wireCapture) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segments := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
		if len(segments) < 2 {
			http.NotFound(w, r)
			return
		}
		prefix := segments[0] + "/" + segments[1]
		if websocket.IsWebSocketUpgrade(r) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			for index := 1; ; index++ {
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				capture.add(prefix, string(data))
				itemID, responseID := fmt.Sprintf("msg_%d", index), fmt.Sprintf("resp_%d", index)
				for _, event := range []string{
					`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"` + itemID + `","role":"assistant","status":"in_progress","content":[]}}`,
					`{"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"` + itemID + `","delta":"answer"}`,
					`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"` + itemID + `","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}}`,
					`{"type":"response.completed","response":{"id":"` + responseID + `","model":"gpt-5.2","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
				} {
					if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
						return
					}
				}
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		beta := "(none)"
		if values, ok := r.Header["Anthropic-Beta"]; ok {
			beta = fmt.Sprintf("%q", values)
		}
		capture.add(prefix, "anthropic-beta "+beta+"\n"+string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":12,\"output_tokens\":5}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(server.Close)
	return server
}

// anthropic-messages.ts:640-642 sends `{ ...nextParams, stream: true }` for any value a payload hook returns, and the SDK (beta/messages.js create) moves betas into the
// anthropic-beta header as `betas?.toString()` and JSON.stringifies the rest. openai-codex-responses.ts:1542 sends JSON.stringify({ type: "response.create", ...requestBody }),
// where a cached continuation is `{ ...body, previous_response_id, input: delta }` (:1472-1476). Pi's own streams send to the same server as PiG's; the recorded bytes and
// headers must be identical.
func TestReplacementAndWebSocketBodiesMatchPiOnTheWire(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for the Pi oracle")
	}
	capture := &wireCapture{requests: map[string][]string{}}
	server := requestBodyOrderServer(t, capture)
	replacements := []string{
		`{"zeta":1,"model":"claude-fable-5-1","betas":["b1","b2"],"messages":[{"role":"user","content":"<x> & y"}],"alpha":{"y":2,"x":1},"stream":false,"max_tokens":5}`,
		`{"model":"claude-fable-5-1","max_tokens":5,"10":"ten","messages":[],"2":"two"}`,
		`{"model":"claude-fable-5-1","betas":"only","dup":1,"max_tokens":5,"dup":2}`,
		`{"model":"claude-fable-5-1","betas":null,"max_tokens":5}`,
		`null`,
		`7`,
		`true`,
		`["a",{"b":1}]`,
		`"h😀"`,
		// Nested values go out as JSON.stringify writes what JSON.parse read: string escapes, a nested repeated key and nested array-index keys, shortest numbers.
		`{"model":"claude-fable-5-1","max_tokens":5,"messages":[],"system":"<&>\u2028\u00e9\ud800","metadata":{"b":{"z":1,"z":3},"10":1,"2":2},"n":1.50,"e":1E2}`,
	}
	type anthropicProbe struct {
		BaseURL     string `json:"baseUrl"`
		Replacement string `json:"replacement"`
	}
	probes := make([]anthropicProbe, len(replacements))
	for i, replacement := range replacements {
		probes[i] = anthropicProbe{BaseURL: fmt.Sprintf("%s/pi/%d", server.URL, i), Replacement: replacement}
	}
	apiKey := codexTestToken(t, "acct_order")
	const sessionID = "session-member-order"
	job, err := json.Marshal(map[string]any{
		"anthropic": probes,
		"codex":     map[string]string{"baseUrl": server.URL + "/pi/codex", "nullBaseUrl": server.URL + "/pi/codexnull", "apiKey": apiKey, "sessionId": sessionID},
	})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/request_body_member_order.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(job)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var piOutcomes struct {
		Anthropic []string `json:"anthropic"`
		Codex     []string `json:"codex"`
	}
	if err := json.Unmarshal(out, &piOutcomes); err != nil {
		t.Fatalf("oracle output %q: %v", out, err)
	}

	for i, replacement := range replacements {
		provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-fable-5-1", ProviderID: "anthropic", BaseURL: fmt.Sprintf("%s/pig/%d", server.URL, i)})
		// The non-object replacements are not Messages requests; the test-binary request check would stop them before the wire.
		stream, err := provider.Stream(requestvalidity.Allow(t.Context()), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{OnPayload: func(any, *Model) (any, error) {
			return json.RawMessage(replacement), nil
		}})
		if err != nil {
			t.Fatalf("replacement %s: %v", replacement, err)
		}
		result := stream.Result()
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
		pi, pig := capture.get(fmt.Sprintf("pi/%d", i)), capture.get(fmt.Sprintf("pig/%d", i))
		if len(pi) != 1 || len(pig) != 1 || pi[0] != pig[0] {
			t.Errorf("replacement %s: Pi (%s) sent\n%q\nPiG (%s) sent\n%q", replacement, piOutcomes.Anthropic[i], pi, result.StopReason, pig)
		}
	}

	hook := func(payload any, _ *Model) (any, error) {
		encoded, err := marshalRequestBody(payload)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(`{"type":"hooked","zeta":{"y":1,"x":2},` + string(encoded[1:len(encoded)-1]) + `,"7":"seven"}`), nil
	}
	defer CloseOpenAICodexWebSocketSessions()
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: apiKey, Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL + "/pig/codex"})
	options := StreamOptions{Transport: TransportWebSocketCached, SessionID: sessionID, OnPayload: hook, WebSocketConnectTimeoutMs: new(5000), TimeoutMs: new(5000)}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	first := stream.Result()
	stream, err = provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}, *first, UserMessage{Content: UserText("two"), Timestamp: 2}}}), options)
	if err != nil {
		t.Fatal(err)
	}
	second := stream.Result()
	pi, pig := capture.get("pi/codex"), capture.get("pig/codex")
	if len(pi) != 2 || len(pig) != 2 {
		t.Fatalf("Codex WebSocket frames: Pi %v sent %d, PiG [%s %s] sent %d\nPi:  %q\nPiG: %q", piOutcomes.Codex, len(pi), first.StopReason, second.StopReason, len(pig), pi, pig)
	}
	for i := range pi {
		if pi[i] != pig[i] {
			t.Errorf("Codex WebSocket frame %d:\nPi  %s\nPiG %s", i+1, pi[i], pig[i])
		}
	}

	provider = NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: apiKey, Model: "gpt-5.2", ProviderID: "openai-codex", BaseURL: server.URL + "/pig/codexnull"})
	stream, err = provider.Stream(requestvalidity.Allow(context.Background()), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("one"), Timestamp: 1}}}), StreamOptions{Transport: TransportWebSocket, WebSocketConnectTimeoutMs: new(5000), TimeoutMs: new(5000), OnPayload: func(any, *Model) (any, error) {
		return json.RawMessage("null"), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	pi, pig = capture.get("pi/codexnull"), capture.get("pig/codexnull")
	if len(pi) != 1 || len(pig) != 1 || pi[0] != pig[0] {
		t.Errorf("null Codex body: Pi (%v) sent %q, PiG (%s) sent %q", piOutcomes.Codex, pi, result.StopReason, pig)
	}
}
